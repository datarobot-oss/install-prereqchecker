/*
   Copyright 2024 the DataRoot team.
   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at
       http://www.apache.org/licenses/LICENSE-2.0
   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package pkg

import (
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-storage-blob-go/azblob"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/gorilla/websocket"
	"github.com/hashicorp/go-version"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	v12 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
	"k8s.io/utils/env"
	"sigs.k8s.io/e2e-framework/klient"
	"sigs.k8s.io/e2e-framework/klient/k8s"
	"sigs.k8s.io/e2e-framework/klient/k8s/resources"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/klient/wait/conditions"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
)

const BYTES_IN_GB = 1024 * 1024 * 1024

const (
	minimumRequiredK8sVersionDefault = "1.23" //TODO pass as an env var
	datarobotTotalPods               = 90
	datarobotTotalServices           = 50
)

var upgrader = websocket.Upgrader{}

func TestNetwork(t *testing.T) {
	if !isTestApplicable(EKS) {
		return
	}
	f := features.New("network").
		Assess("network has enough free IPs", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			environment := getEnvironment()
			var err error
			switch environment {
			case EKS:
				err = testNetworkCapacityEKS()
			default:
				err = testNetworkCapacityOnPrem(cfg.Client())
			}
			if err != nil {
				t.Fatalf("network capacity is not enough %v", err)
			}
			return ctx
		}).
		Assess("ingress correctly configured", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			ingressExternalLBURL, err := getDefaultIngressExternalLBURL(cfg.Client().Resources())
			if err != nil {
				t.Fatalf("Failed to get default ingress external LoadBalancer IP: %v", err)
			}
			err = checkConnectionToRootRoute(&ingressExternalLBURL)
			if err != nil {
				t.Fatalf("Can't connect to default ingress cluster IP: %v", err)
			}
			return ctx
		}).
		Assess("websockets are allowed through ingress", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			wsURL, err := setupWsURL(cfg.Client().Resources(), ctx.Value(nsKey(t)).(string))
			if err != nil {
				t.Fatalf("Can't setup necessary infra for testing websockets: %v", err)
			}
			ingressExternalLBURL, err := getDefaultIngressExternalLBURL(cfg.Client().Resources())
			if err != nil {
				t.Fatalf("Failed to get default ingress external LoadBalancer IP: %v", err)
			}
			externalWsURL := strings.Replace(fmt.Sprintf("%s%s", ingressExternalLBURL, *wsURL), "http", "ws", 1)
			if err := testWSConnection(err, &externalWsURL, t); err != nil {
				t.Fatalf("Failed connecting to websocket server: %v", err)
			}
			return ctx
		}).
		Assess("ingress controllers have required capability", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			var ingressRequirements = map[string]map[string]string{}
			ingressRequirements["apigateway"] = map[string]string{
				"nginx.ingress.kubernetes.io/proxy-body-size":    "1124M",
				"nginx.ingress.kubernetes.io/proxy-read-timeout": "600",
			}
			ingressRequirements["core"] = map[string]string{
				"nginx.ingress.kubernetes.io/proxy-body-size":    "20G",
				"nginx.ingress.kubernetes.io/proxy-read-timeout": "600",
			}
			ingressRequirements["nbx-ingress"] = map[string]string{
				"nginx.ingress.kubernetes.io/proxy-body-size": "1124M",
			}
			ingressRequirements["nbx-websocket"] = map[string]string{
				"nginx.ingress.kubernetes.io/proxy-body-size":    "15m",
				"nginx.ingress.kubernetes.io/proxy-read-timeout": "3600",
			}

			ingressItems, err := getAllIngressControllers(cfg.Client().Resources())
			if err != nil {
				t.Fatalf("Failed to get ingress controllers: %v", err)
				return ctx
			}

			for _, ingItem := range *ingressItems {
				annotations, exists := ingressRequirements[ingItem.Name]
				if exists {
					for annName, annotation := range ingItem.Annotations {
						if val, ok := annotations[annName]; ok {
							if val != annotation {
								t.Fatalf("Ingress controller \"%s\" of \"%s\" has wrong annotation \"%s\", expected \"%s\", got \"%s\"", ingItem.Name, ingItem.Namespace, annName, val, annotation)
							}
						}
					}
				}
			}

			return ctx
		})

	testenv.Test(t, f.Feature())
}

func testWSConnection(err error, wsURL *string, t *testing.T) error {
	// Connect to the server
	ws, _, err := websocket.DefaultDialer.Dial(*wsURL, nil)
	if err != nil {
		t.Fatalf("%v", err)
	}
	defer ws.Close()

	//read first message which is usually "Request served by websocket-server"
	_, _, err = ws.ReadMessage()
	if err != nil {
		return err
	}
	// Send message to server, read response and check to see if it's what we expect.
	for i := 0; i < 10; i++ {
		if err := ws.WriteMessage(websocket.TextMessage, []byte("hello")); err != nil {
			return err
		}
		_, p, err := ws.ReadMessage()
		if err != nil {
			return err
		}
		if string(p) != "hello" {
			return fmt.Errorf("bad message received from WS server: %v", string(p))
		}
	}
	return nil
}

func echo(w http.ResponseWriter, r *http.Request) {
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer c.Close()
	for {
		mt, message, err := c.ReadMessage()
		if err != nil {
			break
		}
		err = c.WriteMessage(mt, message)
		if err != nil {
			break
		}
	}
}

func checkConnectionToRootRoute(rootUrl *string) error {
	resp, err := http.Get(*rootUrl)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK &&
		resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("received bad status code %d from default ingress at URL %s", resp.StatusCode, *rootUrl)
	}
	return nil
}

func getAllIngressControllers(r *resources.Resources) (*[]v12.Ingress, error) {
	ingressList := v12.IngressList{}
	err := r.List(context.TODO(), &ingressList)
	if err != nil {
		return nil, err
	}
	return &ingressList.Items, nil
}

func getDefaultIngressExternalLBURL(r *resources.Resources) (string, error) {
	ingressClassesList := v12.IngressClassList{}
	err := r.List(context.TODO(), &ingressClassesList)
	if err != nil {
		return "", nil
	}
	if len(ingressClassesList.Items) == 0 {
		err := fmt.Errorf("no Ingress Classes found")
		return "", err
	}
	var defaultIngClass *v12.IngressClass = nil
	for _, ingClass := range ingressClassesList.Items {
		if isDefault(ingClass) {
			if defaultIngClass != nil {
				err := fmt.Errorf("multiple default ingress classes defined")
				return "", err
			}
			defaultIngClass = &ingClass
		}
	}
	if defaultIngClass == nil {
		err := fmt.Errorf("no default ingress classes defined")
		return "", err
	}
	candidateIngServices, err := fuzzyFindServices(r, defaultIngClass)
	if err != nil {
		return "", err
	}
	candidateIngServicesWithLB := filter(candidateIngServices, func(s *corev1.Service) bool {
		return s.Spec.Type == "LoadBalancer"
	})
	if candidateIngServicesWithLB == nil || len(candidateIngServicesWithLB) == 0 {
		err = fmt.Errorf("Can not find suitable Ingress Controller services for ingress class %s, found the following candidates: %v",
			defaultIngClass.Name, candidateIngServices)
		return "", err
	}
	ingService := candidateIngServicesWithLB[0] //TODO: should we check all?

	if ingService.Spec.Type != "LoadBalancer" ||
		len(ingService.Status.LoadBalancer.Ingress) == 0 ||
		len(firstNonEmpty(ingService.Status.LoadBalancer.Ingress[0].IP, ingService.Status.LoadBalancer.Ingress[0].Hostname)) == 0 {
		err = fmt.Errorf("Ingress Service %s is not exposed as LoadBalancer service, this MIGHT be a problem", ingService.Name)
		return "", err
	}
	loadBalancerIngress := ingService.Status.LoadBalancer.Ingress[0]
	address := firstNonEmpty(loadBalancerIngress.IP, loadBalancerIngress.Hostname)
	tcpPorts := filter(ingService.Spec.Ports, func(port corev1.ServicePort) bool {
		return port.Protocol == corev1.ProtocolTCP
	})
	httpPorts := filter(tcpPorts, func(port corev1.ServicePort) bool {
		return strings.ToLower(port.Name) == "http" || port.Port == 80
	})
	if len(httpPorts) > 0 {
		address = fmt.Sprintf("http://%s:%d", address, httpPorts[0].Port)
	} else if len(tcpPorts) != 0 {
		address = fmt.Sprintf("http://%s:%d", address, tcpPorts[0].Port)
	}
	return address, nil
}

func firstNonEmpty(args ...string) string {
	for _, s := range args {
		if len(s) != 0 {
			return s
		}
	}
	return ""
}

func isDefault(ingClass v12.IngressClass) bool {
	return ingClass.Name == "nginx" //TODO: remove me after we get rid of nginx

	/*ingClass.Spec.Parameters == nil || (ingClass.Spec.Parameters.Namespace == nil ||
	*ingClass.Spec.Parameters.Namespace == "default")*/ //&&
	//ingClass.Annotations["ingressclass.kubernetes.io/is-default-class"] == "true"
}

func fuzzyFindServices(r *resources.Resources, defaultIngClass *v12.IngressClass) ([]*corev1.Service, error) {
	var res []*corev1.Service
	svcList := corev1.ServiceList{}
	err := r.List(context.TODO(), &svcList)
	fuzzyIngName := defaultIngClass.Name
	if err != nil {
		return nil, err
	}
	for _, svc := range svcList.Items {
		if strings.Contains(svc.Labels["app.kubernetes.io/name"], fuzzyIngName) {
			res = append(res, svc.DeepCopy())
		}
	}
	if res == nil || len(res) == 0 {
		err := fmt.Errorf("No Service for IngressClass %s found", defaultIngClass.Name)
		return nil, err
	}
	return res, nil
}

func testNetworkCapacityOnPrem(c klient.Client) error {
	podList := corev1.PodList{}
	err := c.Resources().List(context.TODO(), &podList)
	if err != nil {
		return err
	}

	servicesList := corev1.ServiceList{}
	err = c.Resources().List(context.TODO(), &servicesList)
	if err != nil {
		return err
	}
	nodeList := corev1.NodeList{}
	err = c.Resources().List(context.TODO(), &nodeList)
	if err != nil {
		return err
	}
	numPods := len(podList.Items)
	numNodes := len(nodeList.Items)
	numServices := len(servicesList.Items)
	//255.255.255.0/24 -> 254 IPs
	numFreeIPs := int64(numNodes*254) - int64(numPods) - int64(numServices)

	totalIPsRequired := getRequiredIPsForDR(0)
	if numFreeIPs < totalIPsRequired {
		err := fmt.Errorf("there is not enough free IPs for DataRobot. Number of free IPs: %d, required: %d",
			numFreeIPs, totalIPsRequired)
		return err
	}
	return nil
}
func testNetworkCapacityEKS() error {
	clusterName := env.GetString("CLUSTER_NAME", "")
	awscfg, err := config.LoadDefaultConfig(context.TODO()) //, config.WithRegion("us-west-2"))
	if err != nil {
		log.Fatalf("unable to load SDK config, %v", err)
	}
	svc := eks.NewFromConfig(awscfg)
	out, err := svc.DescribeCluster(context.TODO(), &eks.DescribeClusterInput{Name: &clusterName})
	if err != nil {
		log.Fatalf("Can't describe cluster %s: %v", clusterName, err)
		return err
	}
	numFreeIPs, err := getNumberOfFreeIPsInVPC(awscfg, out.Cluster.ResourcesVpcConfig.SubnetIds)
	if err != nil {
		log.Fatalf("Can't get number of free IPs: %v", err)
		return err
	}
	totalIPsRequired := getRequiredIPsForDR(len(out.Cluster.ResourcesVpcConfig.SubnetIds))
	if numFreeIPs < totalIPsRequired {
		err := fmt.Errorf("there is not enough free IPs in VPC %s for DataRobot. Number of free IPs: %d, required: %d",
			*out.Cluster.ResourcesVpcConfig.VpcId, numFreeIPs, totalIPsRequired)
		return err
	}
	return nil
}

func getRequiredIPsForDR(numSubnets int) int64 {
	return datarobotTotalPods + datarobotTotalServices + int64(numSubnets)*8
}

func getNumberOfFreeIPsInVPC(cfg aws.Config, subnetIds []string) (int64, error) {
	ec2Client := ec2.NewFromConfig(cfg)
	// Get all subnets in the specified VPC
	subnetsOutput, err := ec2Client.DescribeSubnets(context.TODO(), &ec2.DescribeSubnetsInput{
		SubnetIds: subnetIds,
	})
	if err != nil {
		return -1, err
	}

	var totalFreeIPs int64

	// Calculate the total number of free IPs in all subnets
	for _, subnet := range subnetsOutput.Subnets {
		totalFreeIPs += int64(*subnet.AvailableIpAddressCount)
	}

	return totalFreeIPs, nil
}

func TestCanAccessBlobStorage(t *testing.T) {
	f := features.New("external services").
		Assess("can access blob storage", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			environment := getEnvironment()
			var err error
			switch environment {
			case EKS:
				err = lsS3Buckets()
			case AKS:
				err = lsAzureBuckets()
			}
			//TODO generic S3 driver TODO: Clayton to  tell me env name for ACCESS_KEY, SECRET_KEY
			if err != nil {
				t.Fatal("Error creating privileged pod", err)
			}
			return ctx
		})
	testenv.Test(t, f.Feature())
}

func lsS3Buckets() error {
	if !isTestApplicable(EKS) {
		return nil
	}
	awscfg, err := config.LoadDefaultConfig(context.TODO()) //, config.WithRegion("us-west-2"))
	if err != nil {
		log.Fatalf("unable to load SDK config, %v", err)
	}

	svc := s3.NewFromConfig(awscfg)
	_, err = svc.ListBuckets(context.TODO(), &s3.ListBucketsInput{})

	return err
}

func lsAzureBuckets() error {
	if !isTestApplicable(AKS) {
		return nil
	}
	accountName, accountKey := os.Getenv("AZURE_STORAGE_ACCOUNT"), os.Getenv("AZURE_STORAGE_ACCESS_KEY")
	credential, err := azblob.NewSharedKeyCredential(accountName, accountKey)
	if err != nil {
		log.Fatal("Failed to create client secret credential:", err)
	}

	pipeline := azblob.NewPipeline(credential, azblob.PipelineOptions{})

	URL, _ := url.Parse(fmt.Sprintf("https://%s.blob.core.windows.net/mycontainer", accountName))
	var containerURL = azblob.NewContainerURL(*URL, pipeline)
	if err != nil {
		log.Fatal("Failed to create container URL:", err)
	}
	listBlob, err := containerURL.ListBlobsFlatSegment(context.TODO(), azblob.Marker{}, azblob.ListBlobsSegmentOptions{})
	if err != nil {
		log.Fatal("Failed to list blobs", err)
	}
	for _, blobInfo := range listBlob.Segment.BlobItems {
		fmt.Print("	Blob name: " + blobInfo.Name + "\n")
	}
	return nil
}

func TestCanRunPrivilegedContainers(t *testing.T) {
	f := features.New("privileged containers").
		Assess("can run privileged containers", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {

			fmt.Printf("Creating a privileged pod")
			err, pod := createPrivilegedPod(cfg.Client().Resources(), ctx.Value(nsKey(t)).(string))
			if err != nil {
				t.Fatal("Error creating privileged pod", err)
			}
			err = wait.For(conditions.New(cfg.Client().Resources()).PodRunning(pod),
				wait.WithImmediate(), wait.WithTimeout(5*time.Minute))
			if err != nil {
				t.Fatal("Error creating privileged pod, pod was not running", err)
			}
			return ctx
		})
	testenv.Test(t, f.Feature())
}

func exists[T any](ss []T, test func(T) bool) bool {
	for _, s := range ss {
		if test(s) {
			return true
		}
	}
	return false
}

func filter[T any](ss []T, test func(T) bool) (ret []T) {
	for _, s := range ss {
		if test(s) {
			ret = append(ret, s)
		}
	}
	return
}

func TestK8sVersion(t *testing.T) {
	f := features.New("cluster version").
		Assess("is greater than minimum supported version", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			discoveryClient, err := discovery.NewDiscoveryClientForConfig(cfg.Client().RESTConfig())
			if err != nil {
				t.Fatalf(" error in discoveryClient %v", err)
			}
			information, err := discoveryClient.ServerVersion()
			if err != nil {
				t.Fatalf("error while fetching server version information: %v", err)
			}
			actualVersion, err := version.NewVersion(information.GitVersion)
			if err != nil {
				t.Fatalf("can't parse cluster version %v", actualVersion.String())
			}
			t.Logf("Cluster has version %s\n", actualVersion.String())
			requiredVersion, _ := version.NewVersion(getEnv("MIN_K8S_VERSION", minimumRequiredK8sVersionDefault))
			if actualVersion.LessThan(requiredVersion) {
				t.Fatalf("unsupported k8s version %s, required %s", actualVersion.String(), requiredVersion.String())
			}

			return ctx
		})
	testenv.Test(t, f.Feature())
}

func TestClusterHasEnoughResourcesStatic(t *testing.T) {
	f := features.New("worker nodes").
		Assess("have enough resources", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			var nodes corev1.NodeList
			err := cfg.Client().Resources().List(context.TODO(), &nodes)
			if err != nil {
				t.Fatalf("Failed to get nodes: %v", err)
			}

			t.Logf("Found %d nodes\n", len(nodes.Items))

			if len(nodes.Items) > 3 {
				//TODO
			}
			availableRamGB := int64(0)
			availableCPUCores := float64(0)
			amd64Nodes := filter(nodes.Items, func(node corev1.Node) bool {
				return node.Status.NodeInfo.Architecture == "amd64"
			})

			for _, node := range amd64Nodes {
				availableRamGB += getAvailableRAM(&node)
				availableCPUCores += node.Status.Allocatable.Cpu().AsApproximateFloat64()
			}

			requiredRamGB, requiredCPUCores := getDRMemCpuRequirements()
			if int64(requiredRamGB) > availableRamGB || float64(requiredCPUCores) > availableCPUCores {
				t.Fatalf("Not enough resources. Required %d GB RAM and %d CPUs, got %d GB and %f CPUs",
					requiredRamGB, requiredCPUCores, availableRamGB, availableCPUCores)
			}
			return ctx
		})

	testenv.Test(t, f.Feature())
}

func getDRMemCpuRequirements() (ramGB int, cpuCores int) {
	return 150, 18
}

func roundFloat(val float64, precision uint) float64 {
	ratio := math.Pow(10, float64(precision))
	return math.Round(val*ratio) / ratio
}

func TestClusterHasEnoughResourcesByPodCreation(t *testing.T) {
	f := features.New("available resources").
		Assess("cluster can run pods with enough resources", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			totalRamGB, totalCPUCores := getDRMemCpuRequirements()

			t.Logf("Created %s", ctx.Value(nsKey(t)))
			podRamGB := float32(5)
			numPods := int(roundFloat(float64(totalRamGB)/float64(podRamGB), 0))
			podCPUCores := roundFloat(float64(totalCPUCores)/float64(numPods), 1)
			r := cfg.Client().Resources()
			pods := make([]corev1.Pod, numPods)
			for i := 0; i < numPods; i++ {
				pod, err := createDefaultPod(r, rndString(5), ctx.Value(nsKey(t)).(string), podRamGB, float32(podCPUCores))
				if err != nil {
					t.Error(err)
				}
				pods[i] = *pod
			}
			podList := &corev1.PodList{
				Items: pods,
			}
			err := wait.For(conditions.New(r).ResourcesMatch(podList,
				func(object k8s.Object) bool {
					pod := object.(*corev1.Pod)
					return pod.Status.Phase == corev1.PodRunning ||
						isPodPendingInsufficientResources(pod)
				}), wait.WithImmediate(), wait.WithTimeout(5*time.Minute))
			if err != nil {
				t.Error(err)
			}
			for _, pod := range podList.Items {
				if pod.Status.Phase != corev1.PodRunning {
					t.Fatalf("Some pods are not running, for example pod %s is in status %s",
						pod.Name, pod.Status.Phase)
				}
			}
			return ctx
		})
	testenv.Test(t, f.Feature())
}

func createDefaultPod(r *resources.Resources, name string, namespace string, podRamGB float32, podCPUCores float32) (*corev1.Pod, error) {
	return createPod(r, "busybox", name, namespace, []string{"sleep", "3600"}, podRamGB, podCPUCores)
}

func rndString(n int) string {
	rand.Seed(time.Now().UnixNano())
	p := make([]byte, n)
	rand.Read(p)
	return fmt.Sprintf("%s", hex.EncodeToString(p))[:n]
}

func isPodPendingInsufficientResources(pod *corev1.Pod) bool {
	if pod.Status.Phase != corev1.PodPending {
		return false
	}
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodScheduled && condition.Status == corev1.ConditionFalse {
			if condition.Reason == corev1.PodReasonUnschedulable &&
				strings.Contains(strings.ToLower(condition.Message), "insufficient") {
				return true
			}
		}
	}
	return false
}

func setupWsURL(r *resources.Resources, namespace string) (*string, error) {
	port := int32(8080)
	pod, err := createPodSync(r, "jmalloc/echo-server", "websocket-server", namespace, nil, 0.1, 0.1)
	if err != nil {
		return nil, err
	}
	service, err := createService(r, pod, port)
	if err != nil {
		return nil, err
	}
	ingress, err := createIngressRouteForServiceSync(r, service, port)
	if err != nil {
		return nil, err
	}
	return &ingress.Spec.Rules[0].IngressRuleValue.HTTP.Paths[0].Path, nil
}
func createService(r *resources.Resources, p *corev1.Pod, port int32) (*corev1.Service, error) {
	service := &corev1.Service{
		ObjectMeta: v1.ObjectMeta{
			Name:        p.Name,
			Namespace:   p.Namespace,
			Annotations: map[string]string{"nginx.org/websocket-services": p.Name},
		},
		Spec: corev1.ServiceSpec{
			Selector: p.ObjectMeta.Labels,
		},
	}
	service.Spec.Ports = []corev1.ServicePort{
		{
			Name:     fmt.Sprintf("service-port-tcp-%d", port),
			Protocol: corev1.ProtocolTCP,
			Port:     port,
		},
	}
	err := r.Create(context.TODO(), service)
	if err != nil {
		return nil, err
	}
	return service, nil
}

func createIngressRouteForServiceSync(r *resources.Resources, s *corev1.Service, port int32) (*networkingv1.Ingress, error) {
	var pathTypeExact = networkingv1.PathTypeExact
	var ingressClassName = "nginx"
	ingress := &networkingv1.Ingress{
		ObjectMeta: v1.ObjectMeta{
			Namespace: s.Namespace,
			Name:      fmt.Sprintf("%s-ingress", s.Name),
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &ingressClassName, //TODO: remove me when we get rid of nginx requirement
			Rules: []networkingv1.IngressRule{
				{
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path:     fmt.Sprintf("/install-prereqchecker/%s", s.Name),
									PathType: &pathTypeExact,
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: s.Name,
											Port: networkingv1.ServiceBackendPort{
												Number: port,
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	err := r.Create(context.TODO(), ingress)
	if err != nil {
		return nil, fmt.Errorf("error creating ingress: %v", err)
	}
	err = wait.For(conditions.New(r).ResourcesMatch(
		&networkingv1.IngressList{Items: []networkingv1.Ingress{*ingress}},
		func(object k8s.Object) bool {
			ingress := object.(*networkingv1.Ingress)
			return len(ingress.Status.LoadBalancer.Ingress) > 0 &&
				len(firstNonEmpty(ingress.Status.LoadBalancer.Ingress[0].IP, ingress.Status.LoadBalancer.Ingress[0].Hostname)) > 0
		}), wait.WithImmediate(), wait.WithTimeout(5*time.Minute))
	if err != nil {
		return nil, err
	}
	return ingress, nil
}

func createPodSync(r *resources.Resources, image string, name string, namespace string, command []string,
	podRamGB float32, podCPUCores float32) (*corev1.Pod, error) {

	pod, err := createPod(r, image, name, namespace, command, podRamGB, podCPUCores)
	if err != nil {
		return nil, err
	}
	err = wait.For(conditions.New(r).PodRunning(pod),
		wait.WithImmediate(), wait.WithTimeout(5*time.Minute))
	if err != nil {
		return nil, err
	}
	return pod, nil
}

func createPod(r *resources.Resources, image string, name string, namespace string, command []string,
	podRamGB float32, podCPUCores float32) (*corev1.Pod, error) {
	pod := &corev1.Pod{
		TypeMeta: v1.TypeMeta{},
		ObjectMeta: v1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			Labels:    map[string]string{"app.kubernetes.io/name": name},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:    name,
					Image:   image,
					Command: command,
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse(fmt.Sprint(podCPUCores)),
							corev1.ResourceMemory: resource.MustParse(fmt.Sprintf("%.1f%s", podRamGB, "Gi")),
						},
					},
				},
			},

			RestartPolicy: corev1.RestartPolicyNever,
		},
	}
	err := r.Create(context.TODO(), pod)
	if err != nil {
		return nil, fmt.Errorf("error creating privileged pod: %v", err)
	}

	return pod, nil
}

func createPrivilegedPod(r *resources.Resources, namespace string) (error, *corev1.Pod) {
	pod := &corev1.Pod{
		TypeMeta: v1.TypeMeta{},
		ObjectMeta: v1.ObjectMeta{
			Namespace: namespace,
			Name:      "privileged-pod-test",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:    "privileged-container",
					Image:   "busybox",
					Command: []string{"sleep", "3600"},
					SecurityContext: &corev1.SecurityContext{
						Privileged: Ptr(true),
						RunAsUser:  Ptr(int64(0)),
					},
				},
			},
			RestartPolicy: corev1.RestartPolicyNever,
		},
	}
	err := r.Create(context.TODO(), pod)

	if err != nil {
		return fmt.Errorf("error creating privileged pod: %v", err), nil
	}

	return nil, pod
}
func Ptr[T any](v T) *T {
	return &v
}

func getAvailableRAM(node *corev1.Node) int64 {
	// Retrieve the available RAM for the node
	allocatableRAM := node.Status.Allocatable[corev1.ResourceMemory]
	return allocatableRAM.Value() / BYTES_IN_GB
}
