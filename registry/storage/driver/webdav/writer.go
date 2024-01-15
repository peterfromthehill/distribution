package webdav

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"log"

	storagedriver "github.com/distribution/distribution/v3/registry/storage/driver"
)

type writer struct {
	driver    *driver
	path      string
	buffer    []byte
	buffSize  int
	closed    bool
	committed bool
	cancelled bool
}

func (d *driver) newWriter(path string) storagedriver.FileWriter {
	w := &writer{
		driver: d,
		path:   path,
	}
	return w
}

func (w *writer) Write(p []byte) (int, error) {
	if w.closed {
		return 0, fmt.Errorf("already closed")
	} else if w.committed {
		return 0, fmt.Errorf("already committed")
	} else if w.cancelled {
		return 0, fmt.Errorf("already cancelled")
	}

	w.driver.mutex.Lock()
	defer w.driver.mutex.Unlock()
	if cap(w.buffer) < len(p)+w.buffSize {
		data := make([]byte, len(w.buffer), len(p)+w.buffSize)
		copy(data, w.buffer)
		w.buffer = data
	}

	w.buffer = w.buffer[:w.buffSize+len(p)]
	n := copy(w.buffer[w.buffSize:w.buffSize+len(p)], p)
	w.buffSize += n

	return n, nil
}

func (w *writer) Size() int64 {
	w.driver.mutex.RLock()
	defer w.driver.mutex.RUnlock()
	err := w.driver.c.Connect()
	if err != nil {
		log.Printf("Size Error: %+v\n", err)
		return 0
	}
	fileInfo, err := w.driver.c.Stat(w.path)
	if err != nil {
		return 0
	}
	return fileInfo.Size()
}

func (w *writer) Close() error {
	if w.closed {
		return fmt.Errorf("already closed")
	}
	w.closed = true

	if err := w.flush(); err != nil {
		return err
	}

	return nil
}

func (w *writer) Cancel(ctx context.Context) error {
	if w.closed {
		return fmt.Errorf("already closed")
	} else if w.committed {
		return fmt.Errorf("already committed")
	}
	w.cancelled = true

	w.driver.mutex.Lock()
	defer w.driver.mutex.Unlock()

	err := w.driver.c.Connect()
	if err != nil {
		return err
	}
	return w.driver.c.Remove(w.path)
}

func (w *writer) Commit(ctx context.Context) error {
	if w.closed {
		return fmt.Errorf("already closed")
	} else if w.committed {
		return fmt.Errorf("already committed")
	} else if w.cancelled {
		return fmt.Errorf("already cancelled")
	}
	w.committed = true

	if err := w.flush(); err != nil {
		return err
	}

	return nil
}

func (w *writer) flush() error {
	w.driver.mutex.Lock()
	defer w.driver.mutex.Unlock()
	bufferReader := bytes.NewReader(w.buffer)
	err := w.driver.c.WriteStream(w.path, bufferReader, fs.ModeAppend)
	if err != nil {
		return err
	}
	w.buffer = []byte{}
	w.buffSize = 0

	return nil
}
