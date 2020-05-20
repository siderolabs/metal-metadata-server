package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"log"
	"net/http"

	jsonpatch "github.com/evanphx/json-patch"
	"github.com/ghodss/yaml"
	metalv1alpha1 "github.com/talos-systems/metal-controller-manager/api/v1alpha1"
	"github.com/talos-systems/metal-metadata-server/pkg/client"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var kubeconfig *string
var port *string

const (
	capiVersion  = "v1alpha3"
	metalVersion = "v1alpha1"
)

func main() {
	kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	port = flag.String("port", "8080", "port to use for serving metadata")
	flag.Parse()

	http.HandleFunc("/configdata", FetchConfig)
	http.ListenAndServe(":"+*port, nil)
}

func FetchConfig(w http.ResponseWriter, r *http.Request) {
	vals := r.URL.Query()
	uuid := vals.Get("uuid")
	if len(uuid) == 0 {
		http.Error(w, "uuid param not found", 500)
	}

	log.Printf("recieved metadata request for uuid: %s", uuid)

	k8sClient, err := client.NewClient(kubeconfig)
	if err != nil {
		http.Error(w, "failed to create k8s clientset", 500)
		return
	}

	metalMachineGVR := schema.GroupVersionResource{
		Group:    "infrastructure.cluster.x-k8s.io",
		Version:  capiVersion,
		Resource: "metalmachines",
	}

	capiMachineGVR := schema.GroupVersionResource{
		Group:    "cluster.x-k8s.io",
		Version:  capiVersion,
		Resource: "machines",
	}

	secretGVR := schema.GroupVersionResource{
		Version:  "v1",
		Resource: "secrets",
	}

	serverGVR := schema.GroupVersionResource{
		Group:    "metal.arges.dev",
		Version:  metalVersion,
		Resource: "servers",
	}

	metalMachineList, err := k8sClient.Resource(metalMachineGVR).Namespace("").List(metav1.ListOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			http.Error(w, err.Error(), 404)
			return
		}

		http.Error(w, err.Error(), 500)
		return
	}

	// Range through all metalMachines, seeing if we can match inventory by UUID
	for _, metalMachine := range metalMachineList.Items {
		serverRefString, _, err := unstructured.NestedString(metalMachine.Object, "spec", "serverRef", "name")
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}

		// Check if server ref isn't set. Assuming this is an unstructured thing where it's not an error, just empty.
		if serverRefString == "" {
			continue
		}

		// If ref matches, fetch the bootstrap data from machine resource that owns this metal machine
		if serverRefString == uuid {
			ownerList, present, err := unstructured.NestedSlice(metalMachine.Object, "metadata", "ownerReferences")
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			if !present {
				http.Error(w, "ownerRef not found for metalMachine", 404)
				return
			}

			ownerMachine, present, err := unstructured.NestedString(ownerList[0].(map[string]interface{}), "name")
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			if !present {
				http.Error(w, "owner machine not found for metalMachine", 404)
				return
			}

			metalMachineNS, present, err := unstructured.NestedString(metalMachine.Object, "metadata", "namespace")
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			if !present {
				http.Error(w, "namespace not found for metalMachine", 404)
				return
			}

			machineData, err := k8sClient.Resource(capiMachineGVR).Namespace(metalMachineNS).Get(ownerMachine, metav1.GetOptions{})
			if err != nil {
				if apierrors.IsNotFound(err) {
					http.Error(w, "machine not found", 404)
					return
				}

				http.Error(w, err.Error(), 500)
				return
			}

			bootstrapSecretName, present, err := unstructured.NestedString(machineData.Object, "spec", "bootstrap", "dataSecretName")
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			if !present {
				http.Error(w, "dataSecretName not found for machine", 404)
				return
			}

			bootstrapSecretData, err := k8sClient.Resource(secretGVR).Namespace(metalMachineNS).Get(bootstrapSecretName, metav1.GetOptions{})
			if err != nil {
				if apierrors.IsNotFound(err) {
					http.Error(w, "bootstrap secret not found", 404)
					return
				}

				http.Error(w, err.Error(), 500)
				return
			}

			bootstrapData, present, err := unstructured.NestedString(bootstrapSecretData.Object, "data", "value")
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			if !present {
				http.Error(w, "bootstrap data not found", 404)
				return
			}

			decodedData, err := base64.StdEncoding.DecodeString(bootstrapData)
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}

			// Convert server uuid to unstructured obj and then to structured obj.
			serverRef, err := k8sClient.Resource(serverGVR).Get(serverRefString, metav1.GetOptions{})
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}

			serverObj := &metalv1alpha1.Server{}

			err = runtime.DefaultUnstructuredConverter.FromUnstructured(serverRef.UnstructuredContent(), serverObj)
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}

			// Check if server object has config patches
			if len(serverObj.Spec.ConfigPatches) > 0 {
				marshalledPatches, err := json.Marshal(serverObj.Spec.ConfigPatches)
				if err != nil {
					http.Error(w, err.Error(), 500)
					return
				}

				jsonDecodedData, err := yaml.YAMLToJSON(decodedData)
				if err != nil {
					http.Error(w, err.Error(), 500)
					return
				}

				patch, err := jsonpatch.DecodePatch(marshalledPatches)
				if err != nil {
					http.Error(w, err.Error(), 500)
					return
				}

				jsonDecodedData, err = patch.Apply(jsonDecodedData)
				if err != nil {
					http.Error(w, err.Error(), 500)
					return
				}

				decodedData, err = yaml.JSONToYAML(jsonDecodedData)
				if err != nil {
					http.Error(w, err.Error(), 500)
					return
				}
			}

			w.Write(decodedData)
			return
		}
	}

	// Made it through all metal machines w/ no result
	http.Error(w, "matching machine not found", 404)
	return
}
