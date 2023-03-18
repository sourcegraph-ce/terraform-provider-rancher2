package rancher2

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	log "github.com/sourcegraph-ce/logrus"
	"math/rand"
	"net/http"
	"os"
	"reflect"
	"sort"
	"strings"
	"time"

	ghodssyaml "github.com/ghodss/yaml"
	gover "github.com/hashicorp/go-version"
	"github.com/rancher/norman/clientbase"
	"github.com/rancher/norman/types"
	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v2"
)

const (
	clusterProjectIDSeparator = ":"
	passDigits                = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_"
	passDefaultLen            = 20
	maxHTTPRedirect           = 5
)

func AreEqual(o, n interface{}) bool {
	return reflect.DeepEqual(o, n)
}

func Base64Encode(s string) string {
	if len(s) == 0 {
		return ""
	}
	data := []byte(s)

	return base64.StdEncoding.EncodeToString(data)
}

func Base64Decode(s string) (string, error) {
	if len(s) == 0 {
		return "", nil
	}
	data, err := base64.StdEncoding.DecodeString(s)

	return string(data), err
}

func IsBase64(s string) bool {
	_, err := base64.StdEncoding.DecodeString(s)
	return err == nil
}

func TrimSpace(val interface{}) string {
	return strings.TrimSpace(val.(string))
}

func ToLower(s string) string {
	return strings.ToLower(s)
}

func GetRandomPass(n int) string {
	rand.Seed(time.Now().Unix())
	b := make([]byte, n)
	for i := range b {
		b[i] = passDigits[rand.Int63()%int64(len(passDigits))]
	}
	return string(b)
}

func HashPasswordString(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("Problem encrypting password: %v", err)
	}
	return string(hash), nil
}

func NewListOpts(filters map[string]interface{}) *types.ListOpts {
	listOpts := clientbase.NewListOpts()
	if filters != nil {
		listOpts.Filters = filters
	}

	return listOpts
}

func DoUserLogin(url, user, pass, ttl, desc, cacert string, insecure bool) (string, string, error) {
	loginURL := url + "-public/localProviders/local?action=login"
	loginData := `{"username": "` + user + `", "password": "` + pass + `", "ttl": ` + ttl + `, "description": "` + desc + `"}`
	loginHead := map[string]string{
		"Accept":       "application/json",
		"Content-Type": "application/json",
	}

	// Login with user and pass
	loginResp, err := DoPost(loginURL, loginData, cacert, insecure, loginHead)
	if err != nil {
		return "", "", err
	}

	if loginResp["type"].(string) != "token" || loginResp["token"] == nil {
		return "", "", fmt.Errorf("Doing  user logging: %s %s", loginResp["type"].(string), loginResp["code"].(string))
	}

	return loginResp["id"].(string), loginResp["token"].(string), nil
}

func DoPost(url, data, cacert string, insecure bool, headers map[string]string) (map[string]interface{}, error) {
	response := make(map[string]interface{})

	if url == "" {
		return response, fmt.Errorf("Doing post: URL is nil")
	}

	jsonBytes := []byte(data)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonBytes))
	if err != nil {
		return response, err
	}

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{}

	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: insecure},
		Proxy:           http.ProxyFromEnvironment,
	}

	if cacert != "" {
		// Get the SystemCertPool, continue with an empty pool on error
		rootCAs, _ := x509.SystemCertPool()
		if rootCAs == nil {
			rootCAs = x509.NewCertPool()
		}

		// Append our cert to the system pool
		if ok := rootCAs.AppendCertsFromPEM([]byte(cacert)); !ok {
			log.Println("No certs appended, using system certs only")
		}
		transport.TLSClientConfig.RootCAs = rootCAs
	}

	client.Transport = transport

	resp, err := client.Do(req)
	if err != nil {
		return response, err
	}
	defer resp.Body.Close()

	body, _ := ioutil.ReadAll(resp.Body)
	err = json.Unmarshal(body, &response)
	if err != nil {
		return response, err
	}

	return response, nil
}

func DoGet(url, username, password, cacert string, insecure bool) ([]byte, error) {
	start := time.Now()

	if url == "" {
		return nil, fmt.Errorf("Doing get: URL is nil")
	}
	log.Println("Getting from ", url)

	client := &http.Client{
		Timeout: time.Duration(10 * time.Second),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxHTTPRedirect {
				return fmt.Errorf("Stopped after %d redirects", maxHTTPRedirect)
			}
			if len(username) > 0 && len(password) > 0 {
				req.SetBasicAuth(username, password)
			}
			return nil
		},
	}

	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: insecure},
		Proxy:           http.ProxyFromEnvironment,
	}

	if cacert != "" {
		// Get the SystemCertPool, continue with an empty pool on error
		rootCAs, _ := x509.SystemCertPool()
		if rootCAs == nil {
			rootCAs = x509.NewCertPool()
		}

		// Append our cert to the system pool
		if ok := rootCAs.AppendCertsFromPEM([]byte(cacert)); !ok {
			log.Println("No certs appended, using system certs only")
		}
		transport.TLSClientConfig.RootCAs = rootCAs
	}
	client.Transport = transport

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("Doing get: %v", err)
	}
	if len(username) > 0 && len(password) > 0 {
		req.SetBasicAuth(username, password)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Doing get: %v", err)
	}
	defer resp.Body.Close()

	// Timings recorded as part of internal metrics
	log.Println("Time to get req: ", float64((time.Since(start))/time.Millisecond), " ms")

	return ioutil.ReadAll(resp.Body)

}

func NormalizeURL(url string) string {
	if url == "" || url == "https://" || url == "http://" {
		return ""
	}

	url = strings.TrimSuffix(url, "/")

	if !strings.HasSuffix(url, "/v3") {
		url = url + "/v3"
	}

	return url
}

func RootURL(url string) string {
	NormalizeURL(url)

	url = strings.TrimSuffix(url, "/v3")

	return url
}

func IsNotFound(err error) bool {
	return clientbase.IsNotFound(err)
}

// IsForbidden checks if the given APIError is a Forbidden HTTP statuscode
func IsForbidden(err error) bool {
	apiError, ok := err.(*clientbase.APIError)
	if !ok {
		return false
	}

	return apiError.StatusCode == http.StatusForbidden
}

// IsNotAllowed checks if the given APIError is a Method Not Allowed HTTP statuscode
func IsNotAllowed(err error) bool {
	apiError, ok := err.(*clientbase.APIError)
	if !ok {
		return false
	}

	return apiError.StatusCode == http.StatusMethodNotAllowed
}

func splitTokenID(token string) string {
	separator := ":"

	if strings.Contains(token, separator) {
		return token[0:strings.Index(token, separator)]
	}

	return token
}

func splitID(id string) (clusterID, resourceID string) {
	separator := "."

	if strings.Contains(id, separator) {
		return id[0:strings.Index(id, separator)], id[strings.Index(id, separator)+1:]
	}
	return "", id
}

func splitRegistryID(id string) (namespaceID, projectID, resourceID string) {
	separator := "."

	result := strings.Split(id, separator)

	switch count := len(result); count {
	case 2:
		return "", result[0], result[1]
	case 3:
		return result[0], result[1], result[2]
	}

	return "", "", id
}

func clusterIDFromProjectID(projectID string) (string, error) {
	if projectID == "" || !strings.Contains(projectID, clusterProjectIDSeparator) {
		return "", fmt.Errorf("[ERROR] Getting clusted ID from project ID: Bad project id format %s", projectID)
	}

	return projectID[0:strings.Index(projectID, clusterProjectIDSeparator)], nil
}

func splitProjectIDPart(id string) (projectID string) {
	id = strings.TrimSuffix(id, clusterProjectIDSeparator)

	if strings.Contains(id, clusterProjectIDSeparator) {
		return id[strings.Index(id, clusterProjectIDSeparator)+1:]
	}

	return ""
}

func splitProjectID(id string) (clusterID, projectID string) {
	id = strings.TrimSuffix(id, clusterProjectIDSeparator)

	if strings.Contains(id, clusterProjectIDSeparator) {
		return id[0:strings.Index(id, clusterProjectIDSeparator)], id
	}

	return id, ""
}

func splitAppID(id string) (projectID, appID string, err error) {
	separator := clusterProjectIDSeparator

	fields := strings.Split(id, separator)

	if len(fields) != 3 {
		return "", "", fmt.Errorf("[ERROR] Getting App ID: Bad project id format %s", id)
	}

	return fields[0] + separator + fields[1], fields[1] + separator + fields[2], nil
}

func toArrayString(in []interface{}) []string {
	out := make([]string, len(in))
	for i, v := range in {
		if v == nil {
			out[i] = ""
			continue
		}
		out[i] = v.(string)
	}
	return out
}

func toArrayInterface(in []string) []interface{} {
	out := make([]interface{}, len(in))
	for i, v := range in {
		out[i] = v
	}
	return out
}

func toMapString(in map[string]interface{}) map[string]string {
	out := make(map[string]string)
	for i, v := range in {
		if v == nil {
			out[i] = ""
			continue
		}
		out[i] = v.(string)
	}
	return out
}

func toMapInterface(in map[string]string) map[string]interface{} {
	out := make(map[string]interface{})
	for i, v := range in {
		out[i] = v
	}
	return out
}

func jsonToMapInterface(in string) (map[string]interface{}, error) {
	out := make(map[string]interface{})
	err := jsonToInterface(in, &out)
	return out, err
}

func jsonToInterface(in string, out interface{}) error {
	if out == nil {
		return nil
	}
	err := json.Unmarshal([]byte(in), out)
	if err != nil {
		return err
	}
	return err
}

func ghodssyamlToMapInterface(in string) (map[string]interface{}, error) {
	out := make(map[string]interface{})
	err := ghodssyaml.Unmarshal([]byte(in), &out)
	if err != nil {
		return nil, err
	}
	return out, err
}

func ghodssyamlToInterface(in string, out interface{}) error {
	if out == nil {
		return nil
	}
	err := ghodssyaml.Unmarshal([]byte(in), out)
	if err != nil {
		return err
	}
	return err
}

func yamlToMapInterface(in string) (map[string]interface{}, error) {
	out := make(map[string]interface{})
	err := yaml.Unmarshal([]byte(in), &out)
	if err != nil {
		return nil, err
	}
	return out, err
}

func yamlToInterface(in string, out interface{}) error {
	if out == nil {
		return nil
	}
	err := yaml.Unmarshal([]byte(in), out)
	if err != nil {
		return err
	}
	return err
}

func mapInterfaceToJSON(in map[string]interface{}) (string, error) {
	if in == nil {
		return "", nil
	}
	return interfaceToJSON(in)
}

func mapInterfaceToYAML(in map[string]interface{}) (string, error) {
	if in == nil {
		return "", nil
	}
	return interfaceToYAML(in)
}

func interfaceToJSON(in interface{}) (string, error) {
	if in == nil {
		return "", nil
	}
	out, err := json.Marshal(in)
	if err != nil {
		return "", err
	}
	return string(out), err
}

func interfaceToYAML(in interface{}) (string, error) {
	if in == nil {
		return "", nil
	}
	out, err := yaml.Marshal(in)
	if err != nil {
		return "", err
	}
	return string(out), err
}

func FileExist(path string) (bool, error) {
	if path == "" {
		return false, nil
	}
	_, err := os.Stat(path)

	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func newTrue() *bool {
	b := true
	return &b
}

func newFalse() *bool {
	b := false
	return &b
}

func IsVersionLessThanl(ver1, ver2 string) (bool, error) {
	v1, err := gover.NewVersion(ver1)
	if err != nil {
		return false, err
	}
	v2, err := gover.NewVersion(ver2)
	if err != nil {
		return false, err
	}
	return v1.LessThan(v2), nil
}

func IsVersionGreaterThanOrEqual(ver1, ver2 string) (bool, error) {
	v1, err := gover.NewVersion(ver1)
	if err != nil {
		return false, err
	}
	v2, err := gover.NewVersion(ver2)
	if err != nil {
		return false, err
	}
	return v1.GreaterThanOrEqual(v2), nil
}

func sortVersions(list map[string]string) ([]*gover.Version, error) {
	var versions []*gover.Version
	for key := range list {
		v, err := gover.NewVersion(key)
		if err != nil {
			return nil, err
		}
		versions = append(versions, v)
	}

	sort.Sort(gover.Collection(versions))
	return versions, nil
}

func getLatestVersion(list map[string]string) (string, error) {
	sorted, err := sortVersions(list)
	if err != nil {
		return "", err
	}

	return sorted[len(sorted)-1].Original(), nil
}
