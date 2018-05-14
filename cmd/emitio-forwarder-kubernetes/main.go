package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/emitio/emitio-forwarder-kubernetes/pkg"
	"github.com/emitio/emitio-forwarder-kubernetes/pkg/emitio"
)

func main() {
	l, _ := zap.NewDevelopment()
	zap.ReplaceGlobals(l)
	ctx, cancel := context.WithCancel(context.Background())
	term := make(chan os.Signal, 1)
	signal.Notify(term, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-term
		cancel()
		<-term
		os.Exit(1)
	}()
	// TODO augment the container bytes with container metadata from k8s api
	// kc, err := k8s.NewInClusterClient()
	// if err != nil {
	// 	zap.L().With(zap.Error(err)).Fatal("creating k8s client")
	// }
	zap.L().Info("dialing emitio")
	conn, err := grpc.DialContext(ctx, "127.0.0.1:3648", grpc.WithInsecure())
	if err != nil {
		zap.L().With(zap.Error(err)).Fatal("grpc dial")
	}
	client := emitio.NewEmitIOClient(conn)
	zap.L().Info("creating file manager")
	m := pkg.Manager{
		EmitIO: client,
		Pods:   map[string]*pkg.PodManager{},
	}
	err = m.Run(ctx)
	if err != nil {
		zap.L().With(zap.Error(err)).Fatal("error from manager")
	}
}
