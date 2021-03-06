package operator

import (
	"time"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	appsv1informers "k8s.io/client-go/informers/apps/v1"
	corev1informers "k8s.io/client-go/informers/core/v1"
	appsv1client "k8s.io/client-go/kubernetes/typed/apps/v1"
	appsv1listers "k8s.io/client-go/listers/apps/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"

	"github.com/openshift/cluster-image-registry-operator/pkg/defaults"
	"github.com/openshift/cluster-image-registry-operator/pkg/resource"
)

type NodeCADaemonController struct {
	appsClient      appsv1client.AppsV1Interface
	daemonSetLister appsv1listers.DaemonSetNamespaceLister
	serviceLister   corev1listers.ServiceNamespaceLister

	cachesToSync []cache.InformerSynced
	queue        workqueue.RateLimitingInterface
}

func NewNodeCADaemonController(
	appsClient appsv1client.AppsV1Interface,
	daemonSetInformer appsv1informers.DaemonSetInformer,
	serviceInformer corev1informers.ServiceInformer,
) *NodeCADaemonController {
	c := &NodeCADaemonController{
		appsClient:      appsClient,
		daemonSetLister: daemonSetInformer.Lister().DaemonSets(defaults.ImageRegistryOperatorNamespace),
		serviceLister:   serviceInformer.Lister().Services(defaults.ImageRegistryOperatorNamespace),
		queue:           workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "NodeCADaemonController"),
	}

	daemonSetInformer.Informer().AddEventHandler(c.eventHandler())
	serviceInformer.Informer().AddEventHandler(c.eventHandler())

	c.cachesToSync = append(c.cachesToSync, daemonSetInformer.Informer().HasSynced)
	c.cachesToSync = append(c.cachesToSync, serviceInformer.Informer().HasSynced)

	return c
}

func (c *NodeCADaemonController) eventHandler() cache.ResourceEventHandler {
	const workQueueKey = "instance"
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { c.queue.Add(workQueueKey) },
		UpdateFunc: func(old, new interface{}) { c.queue.Add(workQueueKey) },
		DeleteFunc: func(obj interface{}) { c.queue.Add(workQueueKey) },
	}
}

func (c *NodeCADaemonController) runWorker() {
	for c.processNextWorkItem() {
	}
}

func (c *NodeCADaemonController) processNextWorkItem() bool {
	obj, shutdown := c.queue.Get()
	if shutdown {
		return false
	}
	defer c.queue.Done(obj)

	klog.V(1).Infof("get event from workqueue")
	if err := c.sync(); err != nil {
		c.queue.AddRateLimited(workqueueKey)
		klog.Errorf("NodeCADaemonController: unable to sync: %s, requeuing", err)
	} else {
		c.queue.Forget(obj)
		klog.Infof("NodeCADaemonController: event from workqueue successfully processed")
	}
	return true
}

func (c *NodeCADaemonController) sync() error {
	gen := resource.NewGeneratorNodeCADaemonSet(c.daemonSetLister, c.serviceLister, c.appsClient)
	return resource.ApplyMutator(gen)
}

func (c *NodeCADaemonController) Run(stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()

	klog.Infof("Starting NodeCADaemonController")
	if !cache.WaitForCacheSync(stopCh, c.cachesToSync...) {
		return
	}

	go wait.Until(c.runWorker, time.Second, stopCh)

	klog.Infof("Started NodeCADaemonController")
	<-stopCh
	klog.Infof("Shutting down NodeCADaemonController")
}
