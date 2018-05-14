package pkg

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/cenkalti/backoff"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/emitio/emitio-forwarder-kubernetes/pkg/emitio"
)

// Manager orchestrates watching pod logs
type Manager struct {
	EmitIO emitio.EmitIOClient
	Pods   map[string]*PodManager
}

// Run executes until the context is cancelled and retrying on failure
func (m *Manager) Run(ctx context.Context) error {
	return backoff.Retry(func() error {
		return m.run(ctx)
	}, backoff.WithContext(
		backoff.NewConstantBackOff(time.Second*15),
		ctx,
	))
}

func (m *Manager) run(ctx context.Context) error {
	const path = "/var/log/pods"
	infosCh, wait := WatchDir(ctx, path)
	for {
		select {
		case <-ctx.Done():
			return nil
		case infos, active := <-infosCh:
			if !active {
				return wait()
			}
			paths := []string{}
			for _, info := range infos {
				if !info.IsDir() {
					continue
				}
				paths = append(paths, filepath.Join(path, info.Name()))
			}
			m.set(ctx, paths)
		}
	}
}

func (m *Manager) set(ctx context.Context, paths []string) {
	pathsm := map[string]struct{}{}
	for _, path := range paths {
		pathsm[path] = struct{}{}
	}
	// creates
	for path := range pathsm {
		if _, ok := m.Pods[path]; !ok {
			pod := NewPodManager(ctx, m.EmitIO, path)
			m.Pods[path] = pod
		}
	}
	// deletes
	next := map[string]*PodManager{}
	for path, pod := range m.Pods {
		if _, ok := pathsm[path]; !ok {
			pod.Close()
			continue
		}
		next[path] = pod
	}
	m.Pods = next
}

type PodManager struct {
	cancel     func()
	Path       string
	Client     emitio.EmitIOClient
	Containers map[string]*ContainerManager
}

func NewPodManager(ctx context.Context, client emitio.EmitIOClient, path string) *PodManager {
	ctx, cancel := context.WithCancel(ctx)
	pm := &PodManager{
		cancel:     cancel,
		Client:     client,
		Path:       path,
		Containers: map[string]*ContainerManager{},
	}
	go func() {
		backoff.Retry(func() error {
			err := pm.run(ctx)
			if err != nil {
				zap.L().With(zap.Error(err), zap.String("path", path)).Debug("error running pod manager")
				return err
			}
			return nil
		}, backoff.WithContext(
			backoff.NewConstantBackOff(time.Second*15),
			ctx,
		))
	}()
	return pm
}

func (pm *PodManager) Close() {
	pm.cancel()
}

func (pm *PodManager) run(ctx context.Context) error {
	zap.L().With(zap.String("path", pm.Path)).Debug("watching dir")
	defer zap.L().With(zap.String("path", pm.Path)).Debug("ending watch on dir")
	infosCh, wait := WatchDir(ctx, pm.Path)
	for {
		select {
		case <-ctx.Done():
			return nil
		case infos, active := <-infosCh:
			if !active {
				return wait()
			}
			paths := []string{}
			for _, info := range infos {
				if info.IsDir() {
					continue
				}
				paths = append(paths, filepath.Join(pm.Path, info.Name()))
			}
			pm.set(ctx, paths)
		}
	}
}

func (pm *PodManager) set(ctx context.Context, paths []string) {
	pathsm := map[string]struct{}{}
	for _, path := range paths {
		// TODO filter non-live containers
		pathsm[path] = struct{}{}
	}
	// creates
	for path := range pathsm {
		if _, ok := pm.Containers[path]; !ok {
			container := NewContainerManager(ctx, pm.Client, path)
			pm.Containers[path] = container
		}
	}
	// deletes
	next := map[string]*ContainerManager{}
	for path, container := range pm.Containers {
		if _, ok := pathsm[path]; !ok {
			container.Close()
			continue
		}
		next[path] = container
	}
	pm.Containers = next
}

type ContainerManager struct {
	cancel func()
	Client emitio.EmitIOClient
	Path   string
}

func (cm *ContainerManager) Close() {
	cm.cancel()
}

func NewContainerManager(ctx context.Context, client emitio.EmitIOClient, path string) *ContainerManager {
	ctx, cancel := context.WithCancel(ctx)
	cm := &ContainerManager{
		cancel: cancel,
		Client: client,
		Path:   path,
	}
	go func() {
		backoff.Retry(func() error {
			zap.L().With(zap.String("path", path)).Debug("running container manager")
			err := cm.run(ctx)
			if err != nil {
				zap.L().With(zap.Error(err), zap.String("path", path)).Debug("error running container manager")
				return err
			}
			return nil
		}, backoff.WithContext(
			backoff.NewConstantBackOff(time.Second*15),
			ctx,
		))
	}()
	return cm
}

func (cm *ContainerManager) run(ctx context.Context) error {
	// hack to avoid feedback loop of logs
	zap.L().With(zap.String("path", cm.Path)).Debug("watching file")
	defer zap.L().With(zap.String("path", cm.Path)).Debug("ending watch on file")
	if strings.Contains(cm.Path, "emitio-agent-mock") {
		return errors.New("no feedback loop plz")
	}
	stream, err := cm.Client.Emit(ctx)
	if err != nil {
		return err
	}
	err = stream.Send(&emitio.EmitInput{
		Inputs: &emitio.EmitInput_Header{
			Header: &emitio.EmitHeader{
				Name:     cm.Path,
				Presence: cm.Path,
			},
		},
	})
	if err != nil {
		return err
	}
	eg, ctx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		// reader just to drain stream
		for {
			_, err := stream.Recv()
			if err != nil {
				return err
			}
		}
	})
	eg.Go(func() error {
		lineCh, wait := TailF(ctx, cm.Path)
		for {
			select {
			case <-ctx.Done():
				return nil
			case line, active := <-lineCh:
				if !active {
					return wait()
				}
				err = stream.Send(&emitio.EmitInput{
					Inputs: &emitio.EmitInput_Body{
						Body: &emitio.EmitBody{
							Body: line,
						},
					},
				})
				if err != nil {
					return err
				}
			}
		}
	})
	return eg.Wait()
}
