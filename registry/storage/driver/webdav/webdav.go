package webdav

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"sync"

	storagedriver "github.com/distribution/distribution/v3/registry/storage/driver"
	"github.com/distribution/distribution/v3/registry/storage/driver/base"
	"github.com/distribution/distribution/v3/registry/storage/driver/factory"
	"github.com/sirupsen/logrus"
	"github.com/studio-b12/gowebdav"
)

const driverName = "webdav"

func init() {
	factory.Register(driverName, &webdavDriverFactory{})
}

// webdavDriverFactory implements the factory.StorageDriverFactory interface.
type webdavDriverFactory struct{}

func (factory *webdavDriverFactory) Create(ctx context.Context, parameters map[string]interface{}) (storagedriver.StorageDriver, error) {
	return FromParameters(parameters)
}

type driver struct {
	c                *gowebdav.Client
	mutex            sync.RWMutex
	accessMutex      map[string]*sync.RWMutex
	accessMutexMutex sync.RWMutex
}

// baseEmbed allows us to hide the Base embed.
type baseEmbed struct {
	base.Base
}

// Driver is a storagedriver.StorageDriver implementation backed by a local map.
// Intended solely for example and testing purposes.
type Driver struct {
	baseEmbed // embedded, hidden base driver.
}

// DriverParameters represents all configuration options available for the
// filesystem driver
type DriverParameters struct {
	baseURL    string
	username   string
	password   string
	httpClient http.Client
}

// FromParameters constructs a new Driver with a given parameters map
// Optional Parameters:
// - baseURL
// - username
// - password
func FromParameters(parameters map[string]interface{}) (*Driver, error) {
	baseURL := parameters["baseURL"]
	username := parameters["username"]
	password := parameters["password"]
	params := DriverParameters{
		baseURL:    fmt.Sprint(baseURL),
		username:   fmt.Sprint(username),
		password:   fmt.Sprint(password),
		httpClient: *http.DefaultClient,
	}
	return New(params), nil
}

// New constructs a new Driver.
func New(params DriverParameters) *Driver {
	c := gowebdav.NewClient(params.baseURL, params.username, params.password)
	return &Driver{
		baseEmbed: baseEmbed{
			Base: base.Base{
				StorageDriver: &driver{
					c:           c,
					accessMutex: make(map[string]*sync.RWMutex),
				},
			},
		},
	}
}

// Implement the storagedriver.StorageDriver interface.
func (d *driver) Name() string {
	return driverName
}

// PutContent stores the []byte content at a location designated by "path".
// This should primarily be used for small objects.
func (d *driver) PutContent(ctx context.Context, path string, content []byte) error {
	logrus.Debugf("PutContent: %s / Bytes: %d", path, len(content))
	d.mutex.Lock()
	defer d.mutex.Unlock()
	err := d.c.Write(path, content, fs.ModeAppend)
	return err
}

// Reader retrieves an io.ReadCloser for the content stored at "path"
// with a given byte offset.
// May be used to resume reading a stream by providing a nonzero offset.
func (d *driver) Reader(ctx context.Context, path string, offset int64) (io.ReadCloser, error) {
	logrus.Debugf("Reader: %s", path)
	d.mutex.Lock()
	defer d.mutex.Unlock()
	return d.c.ReadStreamRange(path, offset, 0)
}

// Stat retrieves the FileInfo for the given path, including the current
// size in bytes and the creation time.
func (d *driver) Stat(ctx context.Context, path string) (storagedriver.FileInfo, error) {
	logrus.Debugf("Stat: %s", path)
	// d.mutex.Lock()
	// defer d.mutex.Unlock()
	fileInfo, err := d.c.Stat(path)
	if err != nil {
		// Maybe it is a directory, we have to test it with a '/' at the end
		tmp_path := strings.TrimSuffix(path, "/") + "/"
		fileInfo, err = d.c.Stat(tmp_path)
		if err != nil {
			// its neither a file or a directory
			logrus.Debugf("Path not found: %v / Fi: %v", err, fileInfo)
			return nil, storagedriver.PathNotFoundError{Path: path}
		}
	}
	fi := storagedriver.FileInfoFields{
		Path:    path,
		IsDir:   fileInfo.IsDir(),
		ModTime: fileInfo.ModTime(),
	}
	if !fi.IsDir {
		fi.Size = fileInfo.Size()
	}
	return storagedriver.FileInfoInternal{FileInfoFields: fi}, nil
}

// List returns a list of the objects that are direct descendants of the
// given path.
func (d *driver) List(ctx context.Context, path string) ([]string, error) {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	dir, err := d.Stat(ctx, path)
	if err != nil {
		return nil, storagedriver.PathNotFoundError{Path: path}
	}
	if !dir.IsDir() {
		return nil, storagedriver.Error{Detail: fmt.Errorf("not a directory")}
	}
	fileInfos, _ := d.c.ReadDir(path)
	files := []string{}
	for _, i := range fileInfos {
		name := i.Name()
		// if i.IsDir() {
		// 	name = strings.TrimSuffix(name, "/") + "/"
		// }
		file := strings.TrimSuffix(path, "/") + "/" + name
		files = append(files, file)
	}
	logrus.Debugf("List: %v", files)
	return files, nil
}

// Move moves an object stored at sourcePath to destPath, removing the
// original object.
// Note: This may be no more efficient than a copy followed by a delete for
// many implementations.
func (d *driver) Move(ctx context.Context, sourcePath string, destPath string) error {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	// err := d.c.Connect()
	// if err != nil {
	// 	return storagedriver.Error{Detail: err}
	// }
	dir, _ := path.Split(destPath)
	err := d.c.MkdirAll(dir, fs.ModeAppend)
	if err != nil {
		return storagedriver.Error{Detail: err}
	}
	err = d.c.Rename(sourcePath, destPath, true)
	if err != nil {
		return storagedriver.Error{Detail: err}
	}
	return nil
}

// Delete recursively deletes all objects stored at "path" and its subpaths.
func (d *driver) Delete(ctx context.Context, path string) error {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	// err := d.c.Connect()
	// if err != nil {
	// 	return storagedriver.Error{Detail: err}
	// }
	return d.c.Remove(path)
}

// RedirectURL returns a URL which the client of the request r may use
// to retrieve the content stored at path. Returning the empty string
// signals that the request may not be redirected.
func (d *driver) RedirectURL(r *http.Request, path string) (string, error) {
	return "", nil
}

// Walk traverses a filesystem defined within driver, starting
// from the given path, calling f on each file.
// If the returned error from the WalkFn is ErrSkipDir and fileInfo refers
// to a directory, the directory will not be entered and Walk
// will continue the traversal.
// If the returned error from the WalkFn is ErrFilledBuffer, processing stops.
func (d *driver) Walk(ctx context.Context, path string, f storagedriver.WalkFn, options ...func(*storagedriver.WalkOptions)) error {
	return storagedriver.WalkFallback(ctx, d, path, f, options...)
}
