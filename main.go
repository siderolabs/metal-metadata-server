package main

import (
	"encoding/base64"
	"flag"
	"log"
	"net/http"

	"github.com/talos-systems/metal-metadata-server/pkg/client"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var kubeconfig *string

func main() {
	kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	flag.Parse()

	http.HandleFunc("/configdata", FetchConfig)
	http.ListenAndServe(":8080", nil)
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
		Version:  "v1alpha2",
		Resource: "metalmachines",
	}

	capiMachineGVR := schema.GroupVersionResource{
		Group:    "cluster.x-k8s.io",
		Version:  "v1alpha2",
		Resource: "machines",
	}

	metalMachineList, err := k8sClient.Resource(metalMachineGVR).Namespace("default").List(metav1.ListOptions{})
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
		invRef, _, err := unstructured.NestedString(metalMachine.Object, "spec", "machineInventoryRef", "name")
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}

		// If ref matches, fetch the bootstrap data from machine resource that owns this metal machine
		if invRef == uuid {
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

			machineData, err := k8sClient.Resource(capiMachineGVR).Namespace("default").Get(ownerMachine, metav1.GetOptions{})
			if err != nil {
				if apierrors.IsNotFound(err) {
					http.Error(w, "machine not found", 404)
					return
				}

				http.Error(w, err.Error(), 500)
				return
			}

			bootstrapData, present, err := unstructured.NestedString(machineData.Object, "spec", "bootstrap", "data")
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

			w.Write(decodedData)
		}
	}
}
