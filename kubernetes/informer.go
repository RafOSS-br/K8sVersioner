package kubernetes

import (
	"context"

	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
)

type HandleInformer struct {
	Add    func(obj interface{})
	Del    func(obj interface{})
	Update func(oldObj, newObj interface{})
}

func Watch(ctx context.Context, informer informers.GenericInformer, h HandleInformer) error {
	_, err := informer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			select {
			case <-ctx.Done():
				return
			default:
				h.Add(obj)
			}
		},
		DeleteFunc: func(obj interface{}) {
			select {
			case <-ctx.Done():
				return
			default:
				h.Del(obj)
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			select {
			case <-ctx.Done():
				return
			default:
				h.Update(oldObj, newObj)
			}
		},
	})
	if err != nil {
		return err
	}
	informer.Informer().Run(ctx.Done())
	return nil
}
