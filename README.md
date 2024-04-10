# install-prereqchecker

install-prereqcheker is a CLI tool to verify target installation environment conformance with DataRobot requirements. It is meant to be an open-source self service tool accessible for anyone including customers. The idea is that before DataRobot is installed a support team member or a customer can run a tool which will analyze their target environment to check if it meets the pre-requisites for installing DataRobot.

# Quick start

install-prereqchecker is a plugin for a third party tool called [Sonobuoy](https://sonobuoy.io/) which is used for Kubernetes conformance testing.

## Install sonobuoy

### Mac
```
brew install sonobuoy
```

### Linux


1. Download Sonobuoy tarball
```
wget https://github.com/vmware-tanzu/sonobuoy/releases/download/v0.57.1/sonobuoy_0.57.1_linux_amd64.tar.gz
```

2. Untar Sonobuoy tarball
```
tar -xvf <RELEASE_TARBALL_NAME>.tar.gz

```

3. Copy Sonobuoy executable to somewhere in your path
```
sudo cp sonobuoy /usr/local/bin
```

### Run it

To run the `install-prereqchecker` execute. 
```
sonobuoy run --plugin "https://raw.githubusercontent.com/datarobot/install-prereqchecker/main/plugin.yaml" \
--plugin-env datarobot-conformance.CLUSTER_NAME=<your-cluster-name> \
--plugin-env datarobot-conformance.AWS_REGION=us-east-1 \
--force-image-pull-policy \
--image-pull-policy=Always \
--plugin-env datarobot-conformance.K8S_ENVIRONMENT=EKS
```

This will run a series of tests that are specific to DataRobot to verify that DataRobot will have the required resources to run properly. 
Set `--plugin-env datarobot-conformance.K8S_ENVIRONMENT` to the proper Kubernetes environment.
The default is `on-prem`. You can find all the latest available options [here](https://github.com/datarobot/install-prereqchecker/blob/main/pkg/main_test.go)

Watch it
```
sonobuoy status
```

When it finishes, you will see:
```
                  PLUGIN     STATUS   RESULT   COUNT                 PROGRESS
   datarobot-conformance   complete   failed       1   Passed:  2, Failed:  4
```

Retrieve the results and delete the sonobuoy namespace. Gather the tar.gz file and provide the results to the Enterprise Deployment Team.
```
sonobuoy retrieve 
kubectl delete ns sonobuoy
```

The downloaded tarball will contain everything you need. Look at `plugins/datarobot-conformance/sonobuoy_results.yaml` for a summary of the results

# What and how it tests

`sonobuoy` is just a wrapper tool to spin up a driver container, run tests and grab results. DataRobot conformance tests are standard Go tests built on top of sonoboy, open source end to end Kubernetes testing framework. The test are open sourced as well for anyone to review. We started with a few tests and will continue to add new ones over time based on requirements and feedback. Customer contributions to the tests are also welcome. 

|Test|What it does|Notes|
|-|-|-|
|`TestNetwork`<br>`/network/network_has_enough_free_IPs`| Verifies that there is enough free IPs to install DataRobot|Assumes DataRobot needs 80 (pods) + 50 (services) IPs. Specific to AWS and non-AWS. For AWS grabs subnets assigned to a cluster and checks free IP number. For non-AWS gets number of Nodes, multiples by 254 and substract total number of pods and services|
|`TestNetwork`<br>`/network/ingress_correctly_configured`| Verifies there’s a functioning default Ingress controller with external LoadBalancer accessible by its external IP/hostname | There’s no direct way to match Service for Ingress to its IngressClass in k8s, we are using fuzzy-name search, i.e. we are looking up a service which has IngressClass substring in its name. This is not very reliable and might cause problems (i.e. if you manually create a service and call it “nginx”)|
|`TestNetwork`<br>`/network/websockets_are_allowed_through_ingress` | Verifies websocket connections are working through the external Ingress LoadBalancer||
|`TestCanAccessBlobStorage`<br>`/external_services/can_access_blob_storage`|Verifies BLOB storage is accessible from pods|Specific cloud environment. For AWS uses IRSA and expects Pods to be able to do `aws s3 ls`|
|`TestCanRunPrivilegedContainers`<br>`/privileged_containers/can_run_privileged_containers`|Verifies a privileged container can be run||
|`TestK8sVersion`<br>`/cluster_version/is_greater_than_minimum_supported_version`|Verifies Kubernetes is at given version or higher|use MIN_K8S_VERSION environment variable to specify desired version|
|`TestClusterHasEnoughResourcesStatic`<br>`/worker_nodes/have_enough_resources`|Verifies Kubernetes cluster has enough CPU and RAM to run DataRobot|Assumes DataRobot needs at least 150GB of RAM and 18 CPU. Computes a sum of allocatable CPU and RAM on all nodes|
|`TestClusterHasEnoughResourcesByPodCreation`<br>`/available_resources/cluster_can_run_pods_with_enough_resources`|Assumes DataRobot needs at least 150GB of RAM and 18 CPU. Tries to start a number of containers each requiring 5 GB of RAM and 1 CPU and watching them all successfully scheduling||


