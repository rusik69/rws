package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/shirou/gopsutil/cpu"
	"github.com/shirou/gopsutil/disk"
	"github.com/shirou/gopsutil/mem"
	etcdClient "go.etcd.io/etcd/client"
	"golang.org/x/net/context"
	"html/template"
	"io/ioutil"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const addr = "0.0.0.0:8888"
const DataDir = "data"
const EtcdHost = "http://pi1:2379"

var LocalHostName, _ = os.Hostname()

var EtcdClient etcdClient.Client

type Host struct {
	Name   string
	Port   string
	Disk   uint64
	Memory uint64
	Cores  uint64
}

type File struct {
	Name     string
	Host     string
	Size     uint64
	Replicas uint64
}

type Container struct {
	Image  string
	Name   string
	Disk   uint64
	Memory uint64
	Cores  uint64
	Host   string
	ID     string
}

type Pod struct {
	Name       string
	Image      string
	Count      uint64
	Cores      uint64
	Memory     uint64
	Disk       uint64
	Containers []Container
}

type Info struct {
	Storage    []File
	Hosts      []Host
	Pods       []Pod
	Containers []Container
}

func Fail(str string, err error, w http.ResponseWriter) {
	fmt.Println(str)
	fmt.Println(err.Error())
	w.WriteHeader(http.StatusInternalServerError)
	w.Write([]byte(str))
	w.Write([]byte(err.Error()))
}

func EtcdCreateKey(name, value string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	kAPI := etcdClient.NewKeysAPI(EtcdClient)
	_, err := kAPI.Create(ctx, name, value)
	return err
}

func EtcdSetKey(name, value string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	kAPI := etcdClient.NewKeysAPI(EtcdClient)
	_, err := kAPI.Set(ctx, name, value, nil)
	return err
}

func EtcdDeleteKey(name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	kAPI := etcdClient.NewKeysAPI(EtcdClient)
	_, err := kAPI.Delete(ctx, name, nil)
	return err
}

func EtcdGetKey(name string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	kAPI := etcdClient.NewKeysAPI(EtcdClient)
	resp, err := kAPI.Get(ctx, name, nil)
	if err != nil {
		return "", err
	}
	return resp.Node.Value, nil
}

func EtcdListDir(name string) (etcdClient.Nodes, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	kAPI := etcdClient.NewKeysAPI(EtcdClient)
	resp, err := kAPI.Get(ctx, name, nil)
	if err != nil {
		return nil, err
	}
	return resp.Node.Nodes, nil
}

func StorageUploadHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Println("storage upload")
	PathSplit := strings.Split(r.URL.Path, "/")
	fileName := PathSplit[len(PathSplit)-1]
	dir, err := EtcdListDir("/rws/storage")
	if err != nil {
		Fail("EtcdListDir error", err, w)
	}
	for _, file := range dir {
		if file.Key == fileName {
			Fail("file already exists", errors.New("File already exists"), w)
		}
	}
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		Fail("request reading error", err, w)
		return
	}
	FileSize := len(body)
	FilePathName := DataDir + "/" + fileName
	files, err3 := StorageList()
	if err3 != nil {
		Fail("storage list error", err3, w)
		return
	}
	var x []File
	err4 := json.Unmarshal([]byte(files), &x)
	if err4 != nil {
		Fail("json.Unmarshal error", err4, w)
	}
	for _, file := range x {
		if file.Name == FilePathName {
			Fail("file already exists", errors.New("file already exists"), w)
			return
		}
	}
	di, err2 := disk.Usage("/")
	if err2 != nil {
		Fail("disk usage get error", err2, w)
		return
	}
	if di.Free > uint64(FileSize) {
		err3 := ioutil.WriteFile(FilePathName, []byte(body), 0644)
		if err3 != nil {
			Fail("file write error", err3, w)
			return
		}
		f := File{fileName, LocalHostName, uint64(FileSize), 1}
		fileBytes, err7 := json.Marshal(f)
		if err7 != nil {
			Fail("json.Marshal error", err7, w)
		}
		err8 := EtcdCreateKey("/rws/storage/"+fileName, string(fileBytes))
		if err8 != nil {
			Fail("EtcdCreateKey error", err8, w)
		}
		fmt.Println("file " + FilePathName + " uploaded")
		w.WriteHeader(http.StatusOK)
		return
	} else {
		hostsListString, err5 := ListHosts()
		if err5 != nil {
			Fail("ListHosts error", err5, w)
			return
		}
		var hostsList []Host
		err4 := json.Unmarshal([]byte(hostsListString), &hostsList)
		if err4 != nil {
			fmt.Println("JsonUnmarshal error")
			return
		}
		for _, host := range hostsList {
			url := fmt.Sprintf("http://%s:%s/host_info", host.Name, host.Port)
			body, err5 := http.Get(url)
			if err5 != nil {
				fmt.Println("get error: " + url)
				fmt.Println(body)
				continue
			}
			var ThatHost Host
			json.NewDecoder(body.Body).Decode(&ThatHost)
			if uint64(FileSize) < ThatHost.Disk {
				fmt.Println("uploading to " + host.Name + ":" + host.Port)
				url := fmt.Sprintf("%s:%s/storage_upload/%s", host.Name, host.Port, FilePathName)
				dat, err6 := http.Post(url, "application/octet-stream", r.Body)
				if err6 != nil {
					fmt.Println("post error: " + url)
					fmt.Println(dat)
					continue
				}
			}
		}
	}
	fmt.Println("file upload error")

}

func StorageDownloadHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Println("storage download")
	PathSplit := strings.Split(r.URL.Path, "/")
	fileName := PathSplit[len(PathSplit)-1]
	fmt.Println("filename: " + fileName)
	dir, err := EtcdListDir("/rws/storage")
	if err != nil {
		Fail("EtcdListDir error", err, w)
	}
	found := false
	for _, f := range dir {
		if f.Key == fileName {
			found = true
		}
	}
	if found == false {
		Fail("file not found", errors.New(""), w)
	}
	fileString, err9 := EtcdGetKey("/rws/storage/" + fileName)
	if err9 != nil {
		Fail("EtcdGetKey error", err9, w)
	}
	var file File
	err10 := json.Unmarshal([]byte(fileString), &file)
	if err10 != nil {
		Fail("json.Unmarshal error", err10, w)
	}
	if file.Host == LocalHostName {
		dat, err1 := ioutil.ReadFile(fmt.Sprintf("data/%s", fileName))
		if err1 != nil {
			Fail("file read error", err1, w)
			return
		}
		_, err2 := w.Write(dat)
		if err2 != nil {
			Fail("request write error", err2, w)
			return
		}
		fmt.Println("file " + fileName + " downloaded")
		w.WriteHeader(http.StatusOK)
		return
	} else {
		url := "http://" + file.Host + "/storage_download/" + file.Name
		body, err3 := http.Get(url)
		if err3 != nil {
			Fail("file get error", err3, w)
		}
		bodyBytes, err4 := ioutil.ReadAll(body.Body)
		if err4 != nil {
			fmt.Println(err4)
			Fail("body read error", err4, w)
		}
		w.Write(bodyBytes)
		w.WriteHeader(http.StatusOK)
		return
	}
}

func StorageRemoveHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Println("storage remove")
	PathSplit := strings.Split(r.URL.Path, "/")
	fileName := PathSplit[len(PathSplit)-1]
	fileString, err := EtcdGetKey("/rws/storage/" + fileName)
	if err != nil {
		Fail("EtcdGetKey error", err, w)
	}
	var file File
	err3 := json.Unmarshal([]byte(fileString), &file)
	if err3 != nil {
		Fail("json.Unmarshal error", err3, w)
	}
	if file.Host == LocalHostName {
		err := os.Remove("data/" + fileName)
		if err != nil {
			Fail("file remove error", err, w)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	} else {
		url := "http://" + file.Host + "/storage_remove/" + fileName
		_, err3 := http.Get(url)
		if err3 != nil {
			Fail("file remove get error", err3, w)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}
	err2 := EtcdDeleteKey("/rws/storage/" + fileName)
	if err2 != nil {
		Fail("EtcdDeleteKey error", err2, w)
	}
	return
}

func StorageList() (string, error) {
	filesNodes, err := EtcdListDir("/rws/storage")
	if err != nil {
		return "", errors.New("EtcdListDir error")
	}
	var l []File
	for _, Key := range filesNodes {
		var x File
		err := json.Unmarshal([]byte(Key.Value), &x)
		if err != nil {
			fmt.Println("json unmarshal error")
			return "", err
		}
		l = append(l, x)
	}
	b, err2 := json.Marshal(l)
	if err2 != nil {
		return "", err2
	}
	return string(b), nil
}

func StorageListHandler(w http.ResponseWriter, _ *http.Request) {
	fmt.Println("storage list")
	s, err := StorageList()
	if err != nil {
		Fail("StorageList error", err, w)
		return
	}
	_, err2 := w.Write([]byte(s))
	if err2 != nil {
		Fail("request write error", err2, w)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func StorageFileSizeHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Println("storage Size")
	PathSplit := strings.Split(r.URL.Path, "/")
	fileName := PathSplit[len(PathSplit)-1]
	fmt.Println("filename: " + fileName)
	found := false
	dir, err := EtcdListDir("/rws/storage")
	if err != nil {
		Fail("EtcdListDir error", err, w)
	}
	for _, Key := range dir {
		var file File
		err := json.Unmarshal([]byte(Key.Value), &file)
		if err != nil {
			fmt.Println("json unmarshal error")
			continue
		}
		if file.Name == fileName {
			found = true
		}
	}
	if found == false {
		Fail("file not found", err, w)
		return
	}
	var f File
	key, err := EtcdGetKey("/rws/storage/" + fileName)
	if err != nil {
		Fail("EtcdGetKey error", err, w)
	}
	err2 := json.Unmarshal([]byte(key), &f)
	if err2 != nil {
		Fail("json.Unmarshal error", err2, w)
	}
	w.Write([]byte(strconv.Itoa(int(f.Size))))
	w.WriteHeader(http.StatusOK)
	return
}

func ListLocalContainers() string {
	fmt.Println("list containers")
	c := client.WithVersion("1.38")
	cli, err := client.NewClientWithOpts(c)
	if err != nil {
		fmt.Println("client create error")
		fmt.Println(err)
		return ""
	}
	containers, err2 := cli.ContainerList(context.Background(), types.ContainerListOptions{})
	if err2 != nil {
		fmt.Println("containerList error")
		fmt.Println(err2)
		return ""
	}
	if len(containers) == 0 {
		fmt.Println("there is no running containers on this Host")
		return ""
	}
	b, err3 := json.Marshal(containers)
	if err3 != nil {
		fmt.Println("json marshal error")
		fmt.Println(err3)
		return ""
	}
	return string(b)
}

func ListAllContainers() (string, error) {
	containersNodes, err := EtcdListDir("/rws/containers")
	if err != nil {
		return "", errors.New("EtcdListDir error")
	}
	var l []Container
	for _, Key := range containersNodes {
		var x Container
		err := json.Unmarshal([]byte(Key.Value), &x)
		if err != nil {
			fmt.Println("json.Unmarshal error")
			return "", err
		}
		l = append(l, x)
	}
	b, err2 := json.Marshal(l)
	if err2 != nil {
		return "", err2
	}
	return string(b), nil
}

func ContainerListHandler(w http.ResponseWriter, _ *http.Request) {
	s := ListLocalContainers()
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(s))
}

func ContainerListAllHandler(w http.ResponseWriter, _ *http.Request) {
	s, err := ListAllContainers()
	if err != nil {
		Fail("ListAllContainers error", err, w)
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(s))
}

func RunContainer(imageName, containerName string) (string, error) {
	fmt.Println("run container")
	ctx := context.Background()
	c := client.WithVersion("1.38")
	cli, err := client.NewClientWithOpts(c)
	if err != nil {
		fmt.Println("client create error")
		fmt.Println(err)
		return "", err
	}
	out, err2 := cli.ImagePull(ctx, imageName, types.ImagePullOptions{})
	if err2 != nil {
		fmt.Println("Image pull error")
		fmt.Println(out)
		fmt.Println(err2)
		return "", err2
	}
	resp, err3 := cli.ContainerCreate(ctx, &container.Config{
		Image: imageName,
	}, nil, nil, containerName)
	if err3 != nil {
		fmt.Println("container create error")
		fmt.Println(resp)
		fmt.Println(err3)
		return "", err3
	}
	err4 := cli.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{})
	if err4 != nil {
		fmt.Println("container start error")
		return "", err4
	}
	var cont Container
	containerBytes, err5 := json.Marshal(cont)
	if err5 != nil {
		return "", err5
	}
	err6 := EtcdCreateKey("/rws/container/"+containerName, string(containerBytes))
	if err6 != nil {
		to := 5 * time.Second
		err7 := cli.ContainerStop(ctx, resp.ID, &to)
		if err7 == nil {
			_ = cli.ContainerRemove(ctx, resp.ID, types.ContainerRemoveOptions{})
		}
		return "", nil

	}
	return resp.ID, nil
}

func StopContainer(containerName string) error {
	fmt.Println("stop container")
	ctx := context.Background()
	c := client.WithVersion("1.38")
	cli, err1 := client.NewClientWithOpts(c)
	if err1 != nil {
		fmt.Println("client create error")
		fmt.Println(err1)
		return err1
	}
	ContainerId, _ := GetContainerId(containerName)
	fmt.Println(ContainerId)
	err2 := cli.ContainerStop(ctx, ContainerId, nil)
	if err2 != nil {
		fmt.Println("container stop error")
		fmt.Println(err2)
		return err2
	}
	return nil
}

func GetContainerId(containerName string) (string, error) {
	fmt.Println("get containerId")
	dir, err := EtcdListDir("/rws/containers/")
	if err != nil {
		return "", err
	}
	found := false
	for _, c := range dir {
		if c.Key == containerName {
			found = true
		}
	}
	if found == false {
		return "", errors.New("container doesn't exist")
	}
	containerString, err2 := EtcdGetKey("/rws/containers/" + containerName)
	if err2 != nil {
		return "", err2
	}
	var c Container
	err3 := json.Unmarshal([]byte(containerString), &c)
	if err3 != nil {
		return "", err3
	}
	return c.ID, nil
}

func RemoveContainer(containerName string) error {
	fmt.Println("remove container")
	ContainerID, err := GetContainerId(containerName)
	if err != nil {
		fmt.Println("get container id error")
		fmt.Println(err)
		return err
	}
	ctx := context.Background()
	c := client.WithVersion("1.38")
	cli, err1 := client.NewClientWithOpts(c)
	if err1 != nil {
		fmt.Println("client create error:")
		fmt.Println(err1)
		return err1
	}
	opts := types.ContainerRemoveOptions{RemoveVolumes: false, RemoveLinks: false, Force: false}
	err2 := cli.ContainerRemove(ctx, ContainerID, opts)
	if err2 != nil {
		fmt.Println("container remove error:")
		fmt.Println(err2)
		return err2
	}
	return nil
}

func ContainerRunHandler(w http.ResponseWriter, r *http.Request) {
	var c Container
	err := json.NewDecoder(r.Body).Decode(&c)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	id, err := RunContainer(c.Image, c.Name)
	if err == nil {
		fmt.Fprintf(w, id)
	} else {
		http.Error(w, err.Error(), 500)
	}
}

func ContainerStopHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Println("ContainerStopHandler")
	var c Container
	err := json.NewDecoder(r.Body).Decode(&c)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	dir, err4 := EtcdListDir("/rws/containers")
	if err4 != nil {
		Fail("EtcdListDir error", err4, w)
		return
	}
	found := false
	for _, k := range dir {
		if k.Key == c.Name {
			found = true
		}
	}
	if found == false {
		Fail("container not found", errors.New(""), w)
		return
	}
	if c.Host == LocalHostName {
		err2 := StopContainer(c.Name)
		if err2 == nil {
			fmt.Fprintf(w, "OK")
		} else {
			Fail("stopContainer failure", err2, w)
			return
		}
	} else {
		url := "http://" + c.Host + "/container_stop/" + c.Name
		body, err3 := http.Get(url)
		if err3 != nil {
			if body.StatusCode != 200 {
				Fail("http.Get status code error: "+string(body.StatusCode), err3, w)
				return
			}
		} else {
			Fail("http.Get error", err3, w)
			return
		}
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
	return
}

func ContainerRemoveHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Println("ContainerRemoveHandler")
	var c Container
	err := json.NewDecoder(r.Body).Decode(&c)
	dir, err4 := EtcdListDir("/rws/containers")
	if err4 != nil {
		Fail("EtcdListDir error", err4, w)
		return
	}
	found := false
	for _, k := range dir {
		if k.Key == c.Name {
			found = true
		}
	}
	if found == false {
		Fail("container not found", errors.New(""), w)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if c.Host == LocalHostName {
		err2 := RemoveContainer(c.Name)
		if err2 == nil {
			fmt.Fprintf(w, "OK")
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, err.Error())
		}
	} else {
		url := "http://" + c.Host + "/container_remove/" + c.Name
		body, err3 := http.Get(url)
		if err3 != nil {
			if body.StatusCode != 200 {
				Fail("http.Get status code error: "+string(body.StatusCode), err3, w)
				return
			}
		} else {
			Fail("http.Get error", err3, w)
			return
		}
	}

}

func AddHost(hostName, port string) error {
	fmt.Println("Host add")
	dir, err := EtcdListDir("/rws/hosts")
	if err != nil {
		return err
	}
	found := false
	for _, node := range dir {
		if hostName == node.Key && node.Value == port {
			found = true
			break
		}
	}
	if found == true {
		return errors.New("host already exists")
	}
	HostInfo, err3 := GetHostInfo(hostName, port)
	if err3 != nil {
		fmt.Println("host info get error")
		return err3
	}
	b, err4 := json.Marshal(HostInfo)
	if err4 != nil {
		fmt.Println("host info json marshal error")
		return err4
	}
	HostInfoString := string(b)
	if found == false {
		err2 := EtcdCreateKey("/rws/hosts/"+hostName+":"+port, HostInfoString)
		if err2 != nil {
			return err2
		}
		fmt.Println("host " + hostName + ":" + port + " added")
	} else {
		fmt.Println("host already exists")
		return errors.New("host already exists")
	}
	return nil
}

func HostAddHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Println("HostAddHandler")
	var h Host
	err := json.NewDecoder(r.Body).Decode(&h)
	if err != nil {
		fmt.Println(err.Error())
		http.Error(w, err.Error(), 500)
		return
	}
	err2 := AddHost(h.Name, h.Port)
	if err2 == nil {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "OK")
	} else {
		Fail("host create error", err2, w)
	}
}

func RemoveHost(hostName, port string) error {
	fmt.Println("remove Host")
	dir, err := EtcdListDir("/rws/hosts")
	if err != nil {
		return err
	}
	found := false
	for _, node := range dir {
		if hostName == node.Key && node.Value == port {
			found = true
			break
		}
	}
	if found == false {
		return errors.New("host not found")
	} else {
		err2 := EtcdDeleteKey("/rws/hosts/" + hostName + ":" + port)
		if err2 != nil {
			return err2
		}
	}
	return nil
}

func HostRemoveHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Println("HostRemoveHandler")
	var h Host
	err := json.NewDecoder(r.Body).Decode(&h)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(err.Error()))
		fmt.Println(err.Error())
		return
	}
	err2 := RemoveHost(h.Name, h.Port)
	if err2 == nil {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "OK")
	} else {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, err2.Error())
	}
}

func ListHosts() (string, error) {
	fmt.Println("ListHosts")
	hosts, err := EtcdListDir("/rws/hosts")
	if err != nil {
		fmt.Println("EtcdListDir error")
		return "", err
	}
	var l []map[string]string
	for _, k := range hosts {
		h := map[string]string{k.Key: k.Value}
		l = append(l, h)
	}
	if len(l) > 0 {
		sm, err2 := json.Marshal(l)
		if err2 != nil {
			return "", err2
		}
		return string(sm), nil
	} else {
		return "{}", nil
	}
}

func HostListHandler(w http.ResponseWriter, _ *http.Request) {
	fmt.Println("HostListHandler")
	s, err := ListHosts()
	if err == nil {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(s))
	} else {
		Fail("ListHosts error", err, w)
	}
}

func HostInfo() (string, error) {
	ci, err1 := cpu.Info()
	if err1 != nil {
		return "", err1
	}
	mi, err2 := mem.VirtualMemory()
	if err2 != nil {
		return "", err2
	}
	di, err3 := disk.Usage("/")
	if err3 != nil {
		return "", err3
	}
	name, err4 := os.Hostname()
	if err4 != nil {
		name = "localhost"
	}
	portSplit := strings.Split(addr, ":")
	port := portSplit[len(portSplit)-1]
	c := Host{name, port, di.Free, mi.Available, uint64(len(ci))}
	b, err := json.Marshal(c)
	return string(b), err
}

func HostInfoHandler(w http.ResponseWriter, _ *http.Request) {
	s, err := HostInfo()
	if err == nil {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, s)
	} else {
		Fail("HostInfo error", err, w)
	}
}

func GetFileSize(filename, host, port string) (uint64, error) {
	fmt.Println("GetFileSize")
	url := fmt.Sprintf("http://%s:%s/storage_file_size/%s", host, port, filename)
	body, err := http.Get(url)
	if err != nil {
		fmt.Println("get error")
		fmt.Println(body.Body)
		return 0, err
	}
	if body.StatusCode != 200 {
		fmt.Println(body.StatusCode)
		fmt.Println("status code error")
		return 0, err
	}
	b, err2 := ioutil.ReadAll(body.Body)
	if err2 != nil {
		fmt.Println(err2)
		fmt.Println("response read error")
		return 0, err2
	}
	i, err3 := strconv.Atoi(string(b))
	if err != nil {
		fmt.Println("atoi error")
		fmt.Println(err3)
		return 0, err3
	}
	return uint64(i), nil
}

func GetHostInfo(host, port string) (Host, error) {
	url := fmt.Sprintf("http://%s:%s/host_info", host, port)
	body, err := http.Get(url)
	if err != nil {
		fmt.Println("get error")
		fmt.Println(body)
		return Host{}, err
	}
	var ThatHost Host
	json.NewDecoder(body.Body).Decode(&ThatHost)
	return ThatHost, nil
}

func GetHostPods(host, port string) ([]Pod, error) {
	url := fmt.Sprintf("http://%s:%s/pod_list", host, port)
	body, err := http.Get(url)
	if err != nil {
		fmt.Println("get error")
		fmt.Println(body)
		return []Pod{}, err
	}
	var ThatHostPods []Pod
	json.NewDecoder(body.Body).Decode(&ThatHostPods)
	for _, pod := range ThatHostPods {
		ThatHostPods = append(ThatHostPods, pod)
	}
	return ThatHostPods, nil
}

func GetHostContainers(host, port string) ([]Container, error) {
	url := fmt.Sprintf("http://%s:%s/container_list", host, port)
	body, err := http.Get(url)
	if err != nil {
		fmt.Println("get error")
		fmt.Println(body)
		return []Container{}, err
	}
	BodyBytes, err2 := ioutil.ReadAll(body.Body)
	if err2 != nil {
		fmt.Println("GetHostContainers error")
		fmt.Println(err2)
		return []Container{}, err2
	}
	if len(BodyBytes) == 0 {
		fmt.Println("no containers running on Host")
		return []Container{}, nil
	}
	var HostContainers []Container
	err3 := json.Unmarshal(BodyBytes, &HostContainers)
	if err3 != nil {
		fmt.Println("json unmarshal error")
		fmt.Println(err3)
	}
	return HostContainers, nil
}

func IndexHandler(w http.ResponseWriter, _ *http.Request) {
	fmt.Println("IndexHandler")
	const tpl = `
<!DOCTYPE html>
<html>
	<head>
		<title>RWS</title>
	</head>
	<body>
		<h2>Storage</h2>
		<table>
			<tr>
				<th>Name</th>
				<th>Size</th>
				<th>Host</th>
			</tr>
			{{range .Storage}}
			<tr>
				<td>{{.Name}}</td>
				<td>{{.Size}}</td>
				<td>{{.Host}}</td>
			</tr>
			{{end}}
		</table>
		<h2>Hosts</h2>
		<table>
			<tr>
				<th>Name</th>
				<th>Cores</th>
				<th>Memory</th>
				<th>Disk</th>
			</tr>
			{{range .Hosts}}
			<tr>
				<td>{{.Name}}</td>
				<td>{{.Cores}}</td>
				<td>{{.Memory}}</td>
				<td>{{.Disk}}</td>
			</tr>
			{{end}}
		</table>
		<h2>Pods</h2>
		<table>
			<tr>
				<th>Name</th>
				<th>Image</th>
				<th>Count</th>
				<th>Cores</th>
				<th>Memory</th>
				<th>DISK</th>
			</tr>
			{{range .Pods}}
			<tr>
				<td>{{.Name}}</td>
				<td>{{.Image}}</td>
				<td>{{.Count}}</td>
				<td>{{.Cores}}</td>
				<td>{{.Memory}}</td>
				<td>{{.Disk}}</td>
			<tr>
			{{end}}
		</table>
		<h2>Containers</h2>
		<table>
			<tr>
				<th>Name</th>
				<th>Image</th>
				<th>Host</th>
				<th>Cores</th>
				<th>Memory</th>
				<th>Disk</th>
			</tr>
			{{range .Containers}}
			<tr>
				<td>{{.Name}}</td>
				<td>{{.Image}}</td>
				<td>{{.Host}}</td>

			<tr>
			{{end}}
		</table>
	</body>
</html>
`
	var info Info

	FilesSplit, err3 := StorageListAll()
	if err3 != nil {
		Fail("StorageListAllError", err3, w)
	}
	FilesSplitSplit := strings.Split(FilesSplit, "\n")
	for _, FileAndHost := range FilesSplitSplit {
		PathSplit := strings.Split(FileAndHost, " ")
		FileName := PathSplit[0]
		HostName := PathSplit[1]
		Port := PathSplit[2]
		FileSize, err := GetFileSize(FileName, HostName, Port)
		if err != nil {
			fmt.Println("GetFileSize error ")
			fmt.Println("file: " + FileName)
			fmt.Println(err)
			continue
		}
		FileStorage := InfoStorage{FileName, FileSize, HostName}
		info.Storage = append(info.Storage, FileStorage)
	}
	for host, port := range hosts {
		HostInfo, err := GetHostInfo(host, port)
		if err != nil {
			fmt.Println("GetHostInfo error")
			fmt.Println("Host: " + host)
			fmt.Println(err)
			continue
		}
		InfoHost := InfoHosts{host, HostInfo.Cores, HostInfo.MEMORY, HostInfo.DISK}
		info.Hosts = append(info.Hosts, InfoHost)
	}
	for host, port := range hosts {
		HostPods, err := GetHostPods(host, port)
		if err != nil {
			fmt.Println("GetHostPods error")
			fmt.Println("Host: " + host)
			fmt.Println(err)
			continue
		}
		for _, Pod := range HostPods {
			info.Pods = append(info.Pods, Pod)
		}
	}
	for host, port := range hosts {
		HostContainers, err := GetHostContainers(host, port)
		if err != nil {
			fmt.Println("GetHostContainers error")
			fmt.Println("Host: " + host)
			fmt.Println(err)
			continue
		}
		for _, Container := range HostContainers {
			info.Containers = append(info.Containers, Container)
		}
	}
	t, err2 := template.New("index").Parse(tpl)
	if err2 != nil {
		fmt.Println("index html rendering error")
		fmt.Println(err2)
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(err2.Error()))
	}
	err3 := t.Execute(w, info)
	if err3 != nil {
		fmt.Println("template error")
		fmt.Println(err3)
	}
	return
}

func PodAddHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Println("pod add")
	var p pod
	err := json.NewDecoder(r.Body).Decode(&p)
	if err != nil {
		fmt.Println("json decoding error")
		fmt.Println(err)
		http.Error(w, err.Error(), 500)
		return
	}
	pods = append(pods, p)
	var i uint64
	for host, port := range hosts {
		if i >= p.count {
			break
		}
		url := fmt.Sprintf("http://%s:%s/host_info", host, port)
		body, err := http.Get(url)
		if err != nil {
			fmt.Println("get error")
			fmt.Println(body)
			continue
		}
		var ThatHost HostConfig
		json.NewDecoder(body.Body).Decode(&ThatHost)
		if ThatHost.DISK >= p.disk &&
			ThatHost.CPUS >= p.cpus &&
			ThatHost.MEMORY >= p.memory {
			url := fmt.Sprintf("http://%s:%s/container_run", host, port)
			c := Container{p.image, p.name + "-" + string(i)}
			b := new(bytes.Buffer)
			json.NewEncoder(b).Encode(c)
			resp, err1 := http.Post(url, "application/json", b)
			if err1 != nil {
				fmt.Println(err1)
				fmt.Println("request error")
				continue
			}
			if resp.StatusCode != 200 {
				fmt.Println("request status code error")
				fmt.Println(resp.StatusCode)
				fmt.Println(resp)
				continue
			}
			body, err2 := ioutil.ReadAll(resp.Body)
			if err2 != nil {
				fmt.Println("response read error")
				fmt.Println(err2)
				continue
			}
			p.ids = append(p.ids, string(body))
			fmt.Println(body)
			i += 1
		}
	}
	fmt.Println("all pod containers running")
	return
}

func PodStopHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Println("pod stop")
	var p pod
	err := json.NewDecoder(r.Body).Decode(&p)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	for _, id := range p.ids {
		for host, port := range hosts {
			url := fmt.Sprintf("http://%s/%s/container_list", host, port)
			body, err := http.Get(url)
			if err != nil {
				fmt.Println("get error")
				fmt.Println("body")
				continue
			}
			if body.StatusCode != 200 {
				fmt.Println(body.StatusCode)
				fmt.Println("status code error")
				continue
			}
			b, err2 := ioutil.ReadAll(body.Body)
			if err2 != nil {
				fmt.Println(err2)
				fmt.Println("response read error")
			}
			RemoteContainersSplit := strings.Split(string(b), "\n")
			for _, RemoteContainer := range RemoteContainersSplit {
				if RemoteContainer == id {
					c := Container{"", id}
					b := new(bytes.Buffer)
					json.NewEncoder(b).Encode(c)
					url := fmt.Sprintf("%s:%s/container_stop", host, port)
					resp, err1 := http.Post(url, "application/json", b)
					if err1 != nil {
						fmt.Println(err1)
						fmt.Println("request error")
						continue
					}
					if resp.StatusCode != 200 {
						fmt.Println(resp.StatusCode)
						fmt.Println(resp)
						fmt.Println("request status code error")
						continue
					}
					_, err2 := ioutil.ReadAll(resp.Body)
					if err2 != nil {
						fmt.Println(err2)
						fmt.Println("response read error")
						continue
					}
				}
			}
		}
	}
	return
}

func ListPods() (string, error) {
	fmt.Println("ListPods")
	pods, err := EtcdListDir("/rws/pods")
	if err != nil {
		fmt.Println("EtcdListDir error")
		return "", err
	}
	var l []map[string]string
	for _, k := range pods {
		p := map[string]string{k.Key: k.Value}
		l = append(l, p)
	}
	if len(l) < 0 {
		return "{}", nil
	}
	sm, err2 := json.Marshal(l)
	if err2 != nil {
		return "", err2
	}
	return string(sm), nil
}

func PodListHandler(w http.ResponseWriter, _ *http.Request) {
	fmt.Println("PodListHandler")
	s, err := ListPods()
	if err != nil {
		Fail("PodsList error", err, w)
	}
	w.WriteHeader(http.StatusInternalServerError)
	w.Write()
	return
}

func PodRemoveHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Println("pod remove")
	var p pod
	err := json.NewDecoder(r.Body).Decode(&p)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	removed := false
	for i, other := range pods {
		if other.name == p.name {
			pods = append(pods[:i], pods[i+1:]...)
			removed = true
		}
	}
	if removed {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(p.name))
	} else {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(p.name))
	}
	return
}

func scheduler() {
	for {
		fmt.Println("run scheduler")
		if len(pods) == 0 {
			fmt.Println("no pods defined")
			time.Sleep(60 * time.Second)
			continue
		}
		for index, p := range pods {
			fmt.Println("Pod " + p.name + " should have " + string(len(p.ids)) + " containers")
			var FoundIDs []string
			for host, port := range hosts {
				url := fmt.Sprintf("http://%s:%s/containers_list", host, port)
				body, err := http.Get(url)
				if err != nil {
					fmt.Println("get error")
					fmt.Println(body.Body)
					continue
				}
				if body.StatusCode != 200 {
					fmt.Println(body.StatusCode)
					fmt.Println("status code error")
					continue
				}
				b, err2 := ioutil.ReadAll(body.Body)
				if err2 != nil {
					fmt.Println(err2)
					fmt.Println("response read error")
					continue
				}
				RemoteContainersSplit := strings.Split(string(b), "\n")
				for _, remoteId := range RemoteContainersSplit {
					for _, id := range p.ids {
						if id == remoteId {
							FoundIDs = append(FoundIDs, id)
							break
						}
					}
				}
			}
			fmt.Println("Pod " + p.name + " have " + string(len(FoundIDs)) + " running containers")
			if len(FoundIDs) < len(p.ids) {
				for IDNum, ID := range p.ids {
					found := false
					for _, FoundID := range FoundIDs {
						if ID == FoundID {
							found = true
							break
						}
					}
					if found == false {
						p.ids = append(p.ids[:IDNum], p.ids[IDNum+1:]...)
					}
				}
				RunNum := len(p.ids) - len(FoundIDs)
				i := 0
				for {
					for host, port := range hosts {
						if i >= RunNum {
							break
						}
						url := fmt.Sprintf("%s:%s/container_run", host, port)
						ContainerNameId := len(p.ids)
						name := p.name + "-" + string(ContainerNameId)
						c := Container{p.image, name}
						b := new(bytes.Buffer)
						json.NewEncoder(b).Encode(c)
						resp, err1 := http.Post(url, "application/json", b)
						if err1 != nil {
							fmt.Println(err1)
							panic("request error")
						}
						if resp.StatusCode != 200 {
							fmt.Println(resp.StatusCode)
							fmt.Println(resp)
							panic("request status code error")
						}
						b2, err2 := ioutil.ReadAll(resp.Body)
						if err2 != nil {
							fmt.Println(err2)
							panic("response read error")
						}
						fmt.Println("run new container for pod " + p.name)
						pods[index].ids = append(pods[index].ids, string(b2))
						i += 1
					}
				}
				if i >= RunNum {
					break
				}
			}
			time.Sleep(60 * time.Second)
		}
	}
}

func main() {
	fmt.Println("starting server")
	etcdCfg := etcdClient.Config{
		Endpoints: []string{EtcdHost},
		Transport: etcdClient.DefaultTransport,
	}
	var err error
	EtcdClient, err = etcdClient.New(etcdCfg)
	if err != nil {
		fmt.Println(err)
		panic("etcd client initialization error")
	}
	go scheduler()
	http.HandleFunc("/storage_upload/", StorageUploadHandler)
	http.HandleFunc("/storage_download/", StorageDownloadHandler)
	http.HandleFunc("/storage_remove/", StorageRemoveHandler)
	http.HandleFunc("/storage_list", StorageListHandler)
	http.HandleFunc("/storage_file_size/", StorageFileSizeHandler)
	http.HandleFunc("/container_run", ContainerRunHandler)
	http.HandleFunc("/container_stop", ContainerStopHandler)
	http.HandleFunc("/container_list", ContainerListHandler)
	http.HandleFunc("/container_list_all", ContainerListAllHandler)
	http.HandleFunc("/container_remove", ContainerRemoveHandler)
	http.HandleFunc("/pod_add", PodAddHandler)
	http.HandleFunc("/pod_stop", PodStopHandler)
	http.HandleFunc("/pod_list", PodListHandler)
	http.HandleFunc("/pod_remove", PodRemoveHandler)
	http.HandleFunc("/host_add", HostAddHandler)
	http.HandleFunc("/host_remove", HostRemoveHandler)
	http.HandleFunc("/host_list", HostListHandler)
	http.HandleFunc("/host_info", HostInfoHandler)
	http.HandleFunc("/", IndexHandler)
	if err := http.ListenAndServe(addr, nil); err != nil {
		panic(err)
	}
}
