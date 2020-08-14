### Note: This repo is deprecated. This functionality is now part of our [Sidero](https://github.com/talos-systems/sidero) project.

# metal-metadata-server

## Intro

The Metal Metadata Server is a project by [Talos Systems](https://www.talos-systems.com/) that provides a [Cluster API](https://github.com/kubernetes-sigs/cluster-api)-aware metadata server for bootstrapping bare metal nodes.
The server will attempt to lookup a given CAPI machine resource, given the UUID of the system (acquired from SMBIOS).
Once the system is found, it will simply return the bootstrap data associated with that machine resource.
This field is located in `.spec.bootstrap.data` if you look at a given machine with `kubectl get machine $MACHINE_NAME -o yaml`.

## Corequisites

There are a few corequisites and assumptions that go into using this project:

- [Metal Controller Manager](https://github.com/talos-systems/metal-controller-manager)
- [Cluster API](https://github.com/kubernetes-sigs/cluster-api)
- [Cluster API Provider Metal](https://github.com/talos-systems/cluster-api-provider-metal)

## Building and Installing

This project can be built simply by running `make release` from the root directory.
Doing so will create a file called `_out/release.yaml`.
If you wish, you can tweak setting for service IPs and things of that nature by editing the release yaml.
This file can then be installed into your management cluster with `kubectl apply -f _out/release.yaml`.

## Usage

The Metal Metadata Server is really quite simple.

Once the server is up and running, you can curl a given UUID to test that it is working.
An example:

```bash
curl http://10.96.0.23/configdata?uuid=00000000-0000-0000-0000-dxxxxxxxxx
```

You can then proceed to create your `environment` CRD for the Metal Controller Manager to use, specifying something like `talos.config=http://10.96.0.23/configdata?uuid=` in the kernel flags.

Note that the uuid param is empty.
This is special behavior in Talos, as it will gather the UUID from SMBIOS information and populate that parameter automatically.
