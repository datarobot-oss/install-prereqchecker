# install-prereqchecker

install-prereqcheker is a CLI tool to verify target installation environment conformance with DataRobot requirements. It is meant to be an open-source self service tool accessible for anyone including customers. The idea is that before DataRobot is installed a support team member or a customer can run a tool which will analyze their target environment to check if it meets the pre-requisites for installing DataRobot.

## How it works

### Install sonobuoy

install-prereqchecker is implemented as a plugin for a third party tool called [Sonobuoy](https://sonobuoy.io/) which is used for Kubernetes conformance testing.

#### Mac
```
brew install sonobuoy
```

#### Linux


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

### Run tests

To run the `install-prereqchecker` execute. 
```
sonobuoy run --plugin "https://raw.githubusercontent.com/datarobot/install-prereqchecker/main/plugin.yaml" \
--plugin-env datarobot-conformance.CLUSTER_NAME=<your-cluster-name> \
--plugin-env datarobot-conformance.AWS_REGION=us-east-1 \
--force-image-pull-policy \
--image-pull-policy=Always \
--plugin-env datarobot-conformance.K8S_ENVIRONMENT=EKS
```

Note: To avoid permission issues use `--kubeconfig` flag, more parameters can be found in the official documentation ([here](https://sonobuoy.io/docs/v0.56.15/cli/sonobuoy_run/) and [here](https://sonobuoy.io/understanding-e2e-tests/)).

This will run a series of tests that are specific to DataRobot to verify that DataRobot will have the required resources to run properly. 
Set `--plugin-env datarobot-conformance.K8S_ENVIRONMENT` to the proper Kubernetes environment.
The default is `on-prem`. You can find all the latest available options [here](https://github.com/datarobot/install-prereqchecker/blob/main/pkg/main_test.go)


### Check status and logs

To verify the current status:

```
sonobuoy status
```

When it finishes, you will see:
```
                  PLUGIN     STATUS   RESULT   COUNT                 PROGRESS
   datarobot-conformance   complete   failed       1   Passed:  2, Failed:  4
```

To check logs quickly perform:

```
sonobuoy logs
```

### Get results

To retrieve the results use the following command, where `output` is a directory for logs

```
sonobuoy retrieve -x output
```

To compress results to an arhive use `-f` flag:

```
sonobuoy retrieve -f file.tar.gz
```

The results have the following file structure:

```
output/
├── hosts/
├── meta/                       – The meta data of the cluster.
├── plugins/                    
|   ├── datarobot-conformance/  - The plugin directory.
|   |   └── results/
|   |       └── global/
|   |           └── out.json    - The file of results that includes test statuses and error messages.
|   ├── definition.json         - The cluster configs that are defined for tests.
|   └── sonobuoy_results.yaml   - The main file of results that includes the base status only.
├── podlogs/                    - logs of pods and Sonobuoy collector.
└── resources/
```

### Delete a sonobuoy assets 

To delete the sonobuoy stuff: 

```
sonobuoy delete --all --wait
```

Also, the following command can delete a sonobuoy namespace:

```
kubectl delete ns sonobuoy-01
```

## What and how it tests

`sonobuoy` is just a wrapper tool to spin up a driver container, run tests and grab results. DataRobot conformance tests are standard Go tests built on top of sonoboy, open source end to end Kubernetes testing framework. The test are open sourced as well for anyone to review. We started with a few tests and will continue to add new ones over time based on requirements and feedback. Customer contributions to the tests are also welcome. 

|Test file name|Description|
|-|-|
|`pkg/main_test.go`| Setup the base environment that includes a separate namespace cluster settings.|
|`pkg/dr-conformance_test.go`| All tests that cover the following aspects: `network`, `external services`, `privileged containers`, `cluster version`, `worker nodes`, `available resources`|


## How to add the new tests

### Install Go dependencies

To install Go packages:

```
make install-deps
```

The local running requires environment variables to connect to Kubernetes cluster:

```
export CLUSTER_NAME="ci-drcore-ue1-02"
export K8S_ENVIRONMENT=EKS
export AWS_REGION=us-east-1
```

To execute tests from the local machine:

```
make run-tests
```

To build and push a docker image:

```
docker build --platform linux/amd64 -t ghcr.io/datarobot-oss/install-prereqchecker:latest .
docker push ghcr.io/datarobot-oss/install-prereqchecker:latest
```

Note: Pay attention on `ghcr.io/datarobot-oss/install-prereqchecker:latest`, it can be replaced with your public repository for the manual testing with Sonobuoy.

To execute tests with Sonobuoy and an own docket image use the following command, where `--plugin-image` overrides a docker image for the plugin:

```
sonobuoy run --plugin "./plugin.yaml" \
--plugin-env datarobot-conformance.CLUSTER_NAME=<your-cluster-name> \
--plugin-env datarobot-conformance.AWS_REGION=us-east-1 \
--force-image-pull-policy \
--image-pull-policy=Always \
--plugin-env datarobot-conformance.K8S_ENVIRONMENT=EKS \
--plugin-image datarobot-conformance:datarobot/install-prereqchecker:latest
```
