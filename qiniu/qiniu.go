package qiniu

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/qiniu/api.v7/auth/qbox"
	"github.com/qiniu/api.v7/storage"
	"github.com/qor/oss"
)

// Client Qiniu storage
type Client struct {
	Config        *Config
	mac           *qbox.Mac
	storageCfg    storage.Config
	bucketManager *storage.BucketManager
	// putPolicy
}

// Config Qiniu client config
type Config struct {
	AccessID      string
	AccessKey     string
	Region        string
	Bucket        string
	Endpoint      string
	UseHTTPS      bool
	UseCdnDomains bool
}

var zonedata = map[string]*storage.Zone{
	"huadong": &storage.ZoneHuadong,
	"huabei":  &storage.ZoneHuabei,
	"huanan":  &storage.ZoneHuanan,
	"beimei":  &storage.ZoneBeimei,
}

func New(config *Config) *Client {

	client := &Client{Config: config, storageCfg: storage.Config{}}

	client.mac = qbox.NewMac(config.AccessID, config.AccessKey)

	if z, ok := zonedata[strings.ToLower(config.Region)]; ok {
		client.storageCfg.Zone = z
	} else {
		panic(fmt.Sprintf("Zone %s is invalid, only support huadong, huabei, huanan, beimei.", config.Region))
	}
	if len(config.Endpoint) == 0 {
		panic("endpoint must be provided.")
	}
	client.storageCfg.UseHTTPS = config.UseHTTPS
	client.storageCfg.UseCdnDomains = config.UseCdnDomains
	client.bucketManager = storage.NewBucketManager(client.mac, &client.storageCfg)

	return client
}

// Get receive file with given path
func (client Client) Get(path string) (file *os.File, err error) {
	urlPath := strings.TrimPrefix(path, "/")

	deadline := time.Now().Add(time.Second * 3600).Unix()
	purl := storage.MakePrivateURL(client.mac, client.Config.Endpoint, urlPath, deadline)

	var res *http.Response
	res, err = http.Get(purl)
	if err != nil {
		return
	}

	// fmt.Println("geting", purl)
	// b, _ := httputil.DumpResponse(res, false)
	// fmt.Println(string(b))

	if res.StatusCode != http.StatusOK {
		err = fmt.Errorf("file %s not found", path)
		return
	}

	if file, err = ioutil.TempFile("/tmp", "qiniu"); err == nil {
		_, err = io.Copy(file, res.Body)
		file.Seek(0, 0)
	}

	return file, err
}

// Put store a reader into given path
func (client Client) Put(urlPath string, reader io.Reader) (r *oss.Object, err error) {
	if seeker, ok := reader.(io.ReadSeeker); ok {
		seeker.Seek(0, 0)
	}

	urlPath = stripPath(urlPath)
	var buffer []byte
	buffer, err = ioutil.ReadAll(reader)
	if err != nil {
		return
	}

	fileType := mime.TypeByExtension(path.Ext(urlPath))
	if fileType == "" {
		fileType = http.DetectContentType(buffer)
	}

	putPolicy := storage.PutPolicy{
		Scope: client.Config.Bucket,
	}

	upToken := putPolicy.UploadToken(client.mac)

	formUploader := storage.NewFormUploader(&client.storageCfg)
	ret := storage.PutRet{}
	dataLen := int64(len(buffer))

	putExtra := storage.PutExtra{
		Params: map[string]string{},
	}
	err = formUploader.Put(context.Background(), &ret, upToken, urlPath, bytes.NewReader(buffer), dataLen, &putExtra)
	if err != nil {
		return
	}

	now := time.Now()
	return &oss.Object{
		Path:             ret.Key,
		Name:             filepath.Base(urlPath),
		LastModified:     &now,
		StorageInterface: client,
	}, err
}

// Delete delete file
func (client Client) Delete(path string) error {
	return client.bucketManager.Delete(client.Config.Bucket, stripPath(path))
}

// List list all objects under current path
func (client Client) List(path string) (objects []*oss.Object, err error) {
	var prefix = stripPath(path)
	var listItems []storage.ListItem
	listItems, _, _, _, err = client.bucketManager.ListFiles(
		client.Config.Bucket,
		prefix,
		"",
		"",
		100,
	)

	if err != nil {
		return
	}

	for _, content := range listItems {
		t := time.Unix(content.PutTime, 0)
		objects = append(objects, &oss.Object{
			Path:             "/" + stripPath(content.Key),
			Name:             filepath.Base(content.Key),
			LastModified:     &t,
			StorageInterface: client,
		})
	}

	return
}

// GetEndpoint get endpoint, FileSystem's endpoint is /
func (client Client) GetEndpoint() string {
	if client.Config.Endpoint != "" {
		return client.Config.Endpoint
	}

	return ""
	// endpoint := client.S3.Endpoint
	// for _, prefix := range []string{"https://", "http://"} {
	// 	endpoint = strings.TrimPrefix(endpoint, prefix)
	// }

	// return client.Config.Bucket + "." + endpoint
}

var urlRegexp = regexp.MustCompile(`(https?:)?//((\w+).)+(\w+)/`)

func stripPath(urlPath string) string {
	if urlRegexp.MatchString(urlPath) {
		if u, err := url.Parse(urlPath); err == nil {
			urlPath = u.Path
		}
	}

	return strings.TrimPrefix(urlPath, "/")
}
