package webdav

import (
	"context"
	"fmt"
	"io"
	"log"

	"github.com/studio-b12/gowebdav"
)

type webdavWriter struct {
	Reader         *io.PipeReader
	size           int64
	writerInternal *io.PipeWriter
	closed         bool
	committed      bool
	cancelled      bool
	client         *gowebdav.Client
	path           string
}

func (d *driver) newWebdavWriter(c *gowebdav.Client, path string) *webdavWriter {
	fileReader, fileInternalWriter := io.Pipe()
	return &webdavWriter{
		Reader:         fileReader,
		writerInternal: fileInternalWriter,
		size:           0, // Size returns the number of bytes written to this FileWriter.
		client:         c,
		path:           path,
	}
}

func (fw *webdavWriter) Write(p []byte) (int, error) {
	//log.Printf("Write(): Path: %s Data: %s\n", fw.path, string(p))
	if fw.closed {
		return 0, fmt.Errorf("already closed")
	} else if fw.committed {
		return 0, fmt.Errorf("already committed")
	} else if fw.cancelled {
		return 0, fmt.Errorf("already cancelled")
	}
	n, err := fw.writerInternal.Write(p)
	if err != nil {
		log.Printf("Write Err: %+v\n", err)
	}
	fw.size += int64(n)
	//log.Printf("fwWrite: Write %d bytes (%d)", len(p), fw.size)
	return n, err
}

// Size returns the number of bytes written to this FileWriter.
func (fw *webdavWriter) Size() int64 {
	log.Printf("fwSize(): %s:%d", fw.path, fw.size)
	return fw.size
}

func (fw *webdavWriter) Close() error {
	if fw.closed {
		return fmt.Errorf("already closed")
	}

	if err := fw.writerInternal.Close(); err != nil {
		return err
	}

	fw.closed = true
	return nil
}

func (fw *webdavWriter) Cancel(ctx context.Context) error {
	if fw.closed {
		return fmt.Errorf("already closed")
	}

	fw.cancelled = true
	fw.writerInternal.Close()
	// err := fw.client.Connect()
	// if err != nil {
	// 	return err
	// }
	return fw.client.Remove(fw.path)
}

func (fw *webdavWriter) Commit(ctx context.Context) error {
	if fw.closed {
		return fmt.Errorf("already closed")
	} else if fw.committed {
		return fmt.Errorf("already committed")
	} else if fw.cancelled {
		return fmt.Errorf("already cancelled")
	}
	log.Printf("fwCommit()")
	fw.committed = true
	return nil
}
