//go:build noresumabledigest
// +build noresumabledigest

package webdav

import (
	"context"
	"errors"
	"io/fs"

	storagedriver "github.com/distribution/distribution/v3/registry/storage/driver"
	"github.com/sirupsen/logrus"
)

// GetContent retrieves the content stored at "path" as a []byte.
// This should primarily be used for small objects.
func (d *driver) GetContent(ctx context.Context, path string) ([]byte, error) {
	logrus.Debugf("GetContent: %s", path)
	d.mutex.Lock()
	defer d.mutex.Unlock()

	bytes, err := d.c.Read(path)
	if err != nil {
		return nil, storagedriver.PathNotFoundError{Path: path}
	}
	logrus.Debugf("GetContent() read %d bytes", len(bytes))
	return bytes, nil
}

// Writer returns a FileWriter which will store the content written to it
// at the location designated by "path" after the call to Commit.
// A path may be appended to if it has not been committed, or if the
// existing committed content is zero length.
//
// The behaviour of appending to paths with non-empty committed content is
// undefined. Specific implementations may document their own behavior.
func (d *driver) Writer(ctx context.Context, path string, append bool) (storagedriver.FileWriter, error) {
	if append {
		return nil, errors.New("bla?")
	}
	logrus.Debugf("Writer: %s", path)

	d.mutex.Lock()
	defer d.mutex.Unlock()
	webdavWriter := d.newWebdavWriter(d.c, path)
	go func(path string, _ *driver) {
		logrus.Debugf("Start Stream")
		err := d.c.WriteStream(path, webdavWriter.Reader, fs.ModeAppend)
		if err != nil {
			logrus.Debugf("WriteStream hat ein Problem: %v", err)
		}
		logrus.Debugf("Writer: Quit Subrouting: %s", path)
	}(path, d)
	return webdavWriter, nil
}
