/*
   Copyright 2021 The Kubernetes Authors.
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
	"fmt"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
	"sigs.k8s.io/e2e-framework/klient/k8s/resources"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
	"testing"
)

const BYTES_IN_GB = 1024 * 1024 * 1024

func TestCanRunPrivilegedContainers(t *testing.T) {
	f := features.New("privileged containers").
		Assess("can run privileged containers", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {

			fmt.Printf("Creating a privileged pod")
			err, pod := createPrivilegedPod(cfg.Client().Resources(), ctx.Value("NS-for-%vTestCanRunPrivilegedContainers").(string))
			if err != nil {
				t.Fatal("Error creating privileged pod", err)
			}
			fmt.Printf("Pod: %v \n", pod)
			return ctx
		})
	testenv.Test(t, f.Feature())
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
				fmt.Printf(" error in discoveryClient %v", err)
			}
			information, err := discoveryClient.ServerVersion()
			if err != nil {
				fmt.Println("Error while fetching server version information", err)
			}
			fmt.Printf("Cluster has version %s.%s\n", information.Major, information.Minor)
			return ctx
		})
	testenv.Test(t, f.Feature())
}

func TestClusterHasEnoughResources(t *testing.T) {
	f := features.New("worker nodes").
		Assess("have enough resources", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			var nodes corev1.NodeList
			err := cfg.Client().Resources().List(context.TODO(), &nodes)
			if err != nil {
				t.Fatalf("Failed to get nodes: %v", err)
			}

			fmt.Printf("Got %d nodes\n", len(nodes.Items))
			totalRamGB := int64(0)
			totalCPUCores := float64(0)
			amd64Nodes := filter(nodes.Items, func(node corev1.Node) bool {
				return node.Status.NodeInfo.Architecture == "amd64"
			})

			for _, node := range amd64Nodes {
				totalRamGB += getAvailableRAM(&node)
				totalCPUCores += node.Status.Allocatable.Cpu().AsApproximateFloat64()
			}

			return ctx
		})

	testenv.Test(t, f.Feature())
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
