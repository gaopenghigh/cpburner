package main

import (
	"context"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"sync"
	"sync/atomic"
	"time"

	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	resourceTypeEvent     = "event"
	resourceTypeConfigMap = "configmap"
	actionCreate          = "create"
	actionList            = "list"
	actionClean           = "clean"
)

var (
	timeout      int64 = 300
	commonPrefix       = "evt"
	globalPrefix       = fmt.Sprintf("%s-%d-%d", commonPrefix, time.Now().Unix(), rand.Intn(9999))
	testMsg            = randomString(1024 * 24) // 24k size msg for each event or configmap

	counterSuccess int64
	counterFailure int64

	concurrency int
	listLimit   int64
)

func main() {
	kubeconfig := flag.String("kubeconfig", "", "Absolute path to the kubeconfig file")
	resourceType := flag.String("resourceType", resourceTypeEvent, "What kind of reource to generate, can be 'event' or 'configmap'")
	resourceCount := flag.Int("resourceCount", 100000, "How many resources to generate")
	flag.IntVar(&concurrency, "concurrency", 100, "clientset concurrency")
	flag.Int64Var(&listLimit, "listLimit", 10000, "Limit in list option")
	action := flag.String("action", actionCreate, "one of 'create', 'list' and 'clean'")
	flag.Parse()

	if *resourceType != resourceTypeEvent && *resourceType != resourceTypeConfigMap {
		fmt.Println("error resourceType")
		os.Exit(1)
	}

	var config *rest.Config
	var err error
	if *kubeconfig == "" {
		config, err = rest.InClusterConfig()
		if err != nil {
			panic(err.Error())
		}
	} else {
		config, err = clientcmd.BuildConfigFromFlags("", *kubeconfig)
		if err != nil {
			panic(err)
		}
	}
	config.QPS = 1000
	config.Burst = 2000
	config.Timeout = time.Second * 300

	go func() {
		for {
			time.Sleep(time.Second * 10)
			showStatus()
		}
	}()

	if *action == actionCreate {
		gen(config, *resourceCount, *resourceType)
	} else if *action == actionClean {
		cleanup(config, *resourceType)
	} else if *action == actionList {
		list(config, *resourceType)
	}

	showStatus()
}

func showStatus() {
	fmt.Printf("success: %d, failure: %d\n", counterSuccess, counterFailure)
}

func gen(config *rest.Config, resourceCount int, resourceType string) {
	ctx := context.Background()
	wg := sync.WaitGroup{}
	count := int(resourceCount / concurrency)
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(prefix string) {
			defer wg.Done()
			clientset, err := kubernetes.NewForConfig(config)
			if err != nil {
				panic(err)
			}
			if resourceType == resourceTypeConfigMap {
				generateConfigMaps(ctx, clientset, prefix, count)
			} else {
				generateEvents(ctx, clientset, prefix, count)
			}
		}(fmt.Sprintf("%s-%d", globalPrefix, i))
	}
	wg.Wait()
}

func cleanup(config *rest.Config, resourceType string) {
	ctx := context.Background()
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err)
	}
	if resourceType == resourceTypeConfigMap {
		cleanConfigMaps(ctx, clientset)
	} else {
		cleanEvents(ctx, clientset)
	}
}

func list(config *rest.Config, resourceType string) {
	ctx := context.Background()
	wg := sync.WaitGroup{}
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			clientset, err := kubernetes.NewForConfig(config)
			if err != nil {
				panic(err)
			}
			if resourceType == resourceTypeConfigMap {
				listConfigMaps(ctx, clientset)
			} else {
				listEvents(ctx, clientset)
			}
		}()
	}
	wg.Wait()
}

func generateEvents(ctx context.Context, clientset *kubernetes.Clientset, namePrefix string, count int) {
	client := clientset.CoreV1().Events(apiv1.NamespaceDefault)
	spec := &apiv1.Event{
		ObjectMeta: metav1.ObjectMeta{},
		Reason:     "CPburnerTest",
		Message:    testMsg,
	}
	for i := 0; i < count; i++ {
		spec.ObjectMeta.Name = fmt.Sprintf("%s-%d", namePrefix, i)
		_, err := client.Create(ctx, spec, metav1.CreateOptions{})
		atomic.AddInt64(&counterSuccess, 1)
		if err != nil {
			atomic.AddInt64(&counterFailure, 1)
		}
	}
}

func generateConfigMaps(ctx context.Context, clientset *kubernetes.Clientset, namePrefix string, count int) {
	client := clientset.CoreV1().ConfigMaps(apiv1.NamespaceDefault)
	spec := &apiv1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{},
		Data:       map[string]string{"CPburnerTest": testMsg},
	}
	for i := 0; i < count; i++ {
		spec.ObjectMeta.Name = fmt.Sprintf("%s-%d", namePrefix, i)
		_, err := client.Create(ctx, spec, metav1.CreateOptions{})
		atomic.AddInt64(&counterSuccess, 1)
		if err != nil {
			atomic.AddInt64(&counterFailure, 1)
		}
	}
}

func cleanConfigMaps(ctx context.Context, clientset *kubernetes.Clientset) {
	client := clientset.CoreV1().ConfigMaps(apiv1.NamespaceDefault)
	continueString := ""
	for {
		cms, err := client.List(ctx, metav1.ListOptions{TimeoutSeconds: &timeout, Limit: listLimit, Continue: continueString})
		if err != nil {
			panic(err)
		}
		if len(cms.Items) == 0 {
			return
		}
		for _, cm := range cms.Items {
			if err := client.Delete(ctx, cm.Name, metav1.DeleteOptions{}); err != nil {
				// fmt.Printf("failed to delete cm %s, error: %s\n", cm.Name, err.Error())
				atomic.AddInt64(&counterFailure, 1)
			} else {
				atomic.AddInt64(&counterSuccess, 1)
			}
		}
		continueString = cms.GetListMeta().GetContinue()
	}
}

func cleanEvents(ctx context.Context, clientset *kubernetes.Clientset) {
	client := clientset.CoreV1().Events(apiv1.NamespaceDefault)
	continueString := ""
	for {
		events, err := client.List(ctx, metav1.ListOptions{TimeoutSeconds: &timeout, Limit: listLimit, Continue: continueString})
		if err != nil {
			panic(err)
		}
		if len(events.Items) == 0 {
			return
		}
		for _, e := range events.Items {
			if err := client.Delete(ctx, e.Name, metav1.DeleteOptions{}); err != nil {
				atomic.AddInt64(&counterFailure, 1)
			} else {
				atomic.AddInt64(&counterSuccess, 1)
			}
		}
		continueString = events.GetListMeta().GetContinue()
	}
}

func listConfigMaps(ctx context.Context, clientset *kubernetes.Clientset) {
	client := clientset.CoreV1().ConfigMaps(apiv1.NamespaceDefault)
	continueString := ""
	for {
		resources, err := client.List(ctx, metav1.ListOptions{TimeoutSeconds: &timeout, Limit: listLimit, Continue: continueString})
		if err != nil {
			// fmt.Println("failed to list: ", err)
			atomic.AddInt64(&counterFailure, 1)
		} else {
			atomic.AddInt64(&counterSuccess, 1)
		}
		if len(resources.Items) == 0 || resources.GetContinue() == "" {
			return
		}
		continueString = resources.GetListMeta().GetContinue()
	}
}

func listEvents(ctx context.Context, clientset *kubernetes.Clientset) {
	client := clientset.CoreV1().Events(apiv1.NamespaceDefault)
	continueString := ""
	for {
		resources, err := client.List(ctx, metav1.ListOptions{TimeoutSeconds: &timeout, Limit: listLimit, Continue: continueString})
		if err != nil {
			atomic.AddInt64(&counterFailure, 1)
		} else {
			atomic.AddInt64(&counterSuccess, 1)
		}
		if len(resources.Items) == 0 || resources.GetContinue() == "" {
			return
		}
		continueString = resources.GetListMeta().GetContinue()
	}
}

func randomString(n int) string {
	var letterBytes = []byte("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")
	b := make([]byte, n)
	for i := range b {
		b[i] = letterBytes[rand.Intn(len(letterBytes))]
	}
	return string(b)
}
