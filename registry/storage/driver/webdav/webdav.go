package webdav

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"path"
	"sync"

	storagedriver "github.com/distribution/distribution/v3/registry/storage/driver"
	"github.com/distribution/distribution/v3/registry/storage/driver/base"
	"github.com/distribution/distribution/v3/registry/storage/driver/factory"
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
	c           *gowebdav.Client
	mutex       sync.RWMutex
	accessMutex map[string]*sync.RWMutex
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

// GetContent retrieves the content stored at "path" as a []byte.
// This should primarily be used for small objects.
func (d *driver) GetContent(ctx context.Context, path string) ([]byte, error) {
	log.Printf("GetContent: %s", path)
	d.mutex.Lock()
	defer d.mutex.Unlock()
	if d.accessMutex[path] == nil {
		var fileMutex sync.RWMutex
		d.accessMutex[path] = &fileMutex
	}
	d.accessMutex[path].Lock()
	defer d.accessMutex[path].Unlock()
	// err := d.c.Connect()
	// if err != nil {
	// 	return nil, storagedriver.PathNotFoundError{Path: path}
	// }
	fi, err := d.Stat(ctx, path)
	log.Printf("GetContent: Fi: %v / Err: %v", fi, err)
	// if fi != nil && fi.Size() == 0 {
	// 	log.Printf("Realy 0? Sleep some seconds and try again")
	// 	time.Sleep(10 * time.Second)
	// 	fi, err := d.Stat(ctx, path)
	// 	log.Printf("GetContent: Fi: %v / Err: %v", fi, err)
	// }
	bytes, err := d.c.Read(path)
	if err != nil {
		return nil, storagedriver.PathNotFoundError{Path: path}
	}
	log.Printf("GetContent() read %d bytes", len(bytes))
	if len(bytes) == 0 {
		// strange
		log.Printf("Realy 0?")
		//time.Sleep(20 * time.Second)
	}
	return bytes, nil
}

// PutContent stores the []byte content at a location designated by "path".
// This should primarily be used for small objects.
func (d *driver) PutContent(ctx context.Context, path string, content []byte) error {
	log.Printf("PutContent: %s", path)
	d.mutex.Lock()
	defer d.mutex.Unlock()
	// err := d.c.Connect()
	// if err != nil {
	// 	return err
	// }
	err := d.c.Write(path, content, fs.ModeAppend)
	return err
}

// Reader retrieves an io.ReadCloser for the content stored at "path"
// with a given byte offset.
// May be used to resume reading a stream by providing a nonzero offset.
func (d *driver) Reader(ctx context.Context, path string, offset int64) (io.ReadCloser, error) {
	log.Printf("Reader: %s", path)
	d.mutex.Lock()
	defer d.mutex.Unlock()
	// err := d.c.Connect()
	// if err != nil {
	// 	return nil, err
	// }
	return d.c.ReadStreamRange(path, offset, 0)
}

// Writer returns a FileWriter which will store the content written to it
// at the location designated by "path" after the call to Commit.
// A path may be appended to if it has not been committed, or if the
// existing committed content is zero length.
//
// The behaviour of appending to paths with non-empty committed content is
// undefined. Specific implementations may document their own behavior.
func (d *driver) Writer(ctx context.Context, path string, append bool) (storagedriver.FileWriter, error) {
	log.Printf("Writer: %s Append: %v", path, append)

	buffer := []byte{}
	var err error
	if append {
		buffer, err = d.GetContent(ctx, path)
		if err != nil {
			log.Printf("Error, assume file not exists: %v", err)
			log.Printf("Buffer: %s", string(buffer))
			buffer = []byte{}
		}
	}
	d.mutex.Lock()
	defer d.mutex.Unlock()
	webdavWriter := d.newWebdavWriter(d.c, path)
	go func(path string, d *driver) {
		if d.accessMutex[path] == nil {
			var fileMutex sync.RWMutex
			d.accessMutex[path] = &fileMutex
		}
		d.accessMutex[path].Lock()
		defer d.accessMutex[path].Unlock()
		log.Printf("Start Stream")
		err := d.c.WriteStream(path, webdavWriter.Reader, fs.ModeAppend)
		if err != nil {
			log.Printf("WriteStream hat ein Problem: %v", err)
		}
		log.Printf("Writer: Quit Subrouting: %s", path)
	}(path, d)
	if append {
		if len(buffer) > 0 {
			log.Printf("Writer(): FIXME: Appending")
			length, err := webdavWriter.Write(buffer)
			if err != nil {
				return nil, err
			}
			if length != len(buffer) {
				return nil, fmt.Errorf("file get total get lost :/ maybe delete?!")
			}
		}
		log.Printf("Seek: %d", webdavWriter.Size())
	}
	return webdavWriter, nil
}

// Stat retrieves the FileInfo for the given path, including the current
// size in bytes and the creation time.
func (d *driver) Stat(ctx context.Context, path string) (storagedriver.FileInfo, error) {
	log.Printf("Stat: %s", path)
	// d.mutex.Lock()
	// defer d.mutex.Unlock()
	// err := d.c.Connect()
	// if err != nil {
	// 	return nil, err
	// }
	fileInfo, err := d.c.Stat(path)
	if err != nil {
		return nil, storagedriver.PathNotFoundError{Path: path}
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
	var files []string
	for _, i := range fileInfos {
		files = append(files, i.Name())
	}
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
