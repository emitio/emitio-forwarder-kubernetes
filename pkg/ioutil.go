package pkg

import (
	"context"
	"io/ioutil"
	"os"

	"github.com/fsnotify/fsnotify"
	"github.com/hpcloud/tail"
	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"
)

const (
	relativeToEnd = 2
)

// TailF reads from the end of the provided file and returns lines as they are written. TailF accommodates
// logrotate where the original tailed file may be moved and a new file takes its place.
// TODO expose the current offset when reading and allow a new call to TailF to specify an offset.
func TailF(ctx context.Context, path string) (<-chan []byte, func() error) {
	out := make(chan []byte)
	eg, ctx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		defer close(out)
		cfg := tail.Config{
			Follow: true,
			Location: &tail.SeekInfo{
				Offset: 0,
				Whence: relativeToEnd,
			},
			Logger: tail.DiscardingLogger,
		}
		t, err := tail.TailFile(path, cfg)
		if err != nil {
			return err
		}
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case line, active := <-t.Lines:
				if !active {
					err = t.Stop()
					if err != nil {
						return err
					}
					t, err = tail.TailFile(path, cfg)
					if err != nil {
						return err
					}
					continue
				}
				select {
				case <-ctx.Done():
					return nil
				case out <- []byte(line.Text):
				}
			}
		}
	})
	return out, eg.Wait
}

// WatchDir watches the provided directory and returns the current state of ReadDir as it changes
func WatchDir(ctx context.Context, path string) (<-chan []os.FileInfo, func() error) {
	out := make(chan []os.FileInfo)
	eg, ctx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		defer close(out)
		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			return errors.Wrap(err, "new watcher")
		}
		defer watcher.Close()
		err = watcher.Add(path)
		if err != nil {
			return errors.Wrap(err, "watcher add path")
		}
		for {
			infos, err := ioutil.ReadDir(path)
			if err != nil {
				return errors.Wrap(err, "read dir")
			}
			select {
			case <-ctx.Done():
				return nil
			case err := <-watcher.Errors:
				return errors.Wrap(err, "watcher error channel")
			case out <- infos:
			}
			select {
			case <-ctx.Done():
				return nil
			case err := <-watcher.Errors:
				return errors.Wrap(err, "watcher error channel")
			case <-watcher.Events:
			}
		}
	})
	return out, eg.Wait
}
