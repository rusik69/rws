package main

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
)

const addr = ":8888"

func storageUploadHandler(w http.ResponseWriter, r *http.Request) {
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		fmt.Println("request reading error")
		return
	}
	pathSplit := strings.Split(r.URL.Path, "/")
	filename := pathSplit[len(pathSplit)-1]
	err3 := ioutil.WriteFile(fmt.Sprintf("%s/%s", dataDir, filename), []byte(body), 0644)
	if err3 != nil {
		fmt.Println("fire write error")
		return
	}
}

func storageDownloadHandler(w http.ResponseWriter, r *http.Request) {
	pathSplit := strings.Split(r.URL.Path, "/")
	filename := pathSplit[len(pathSplit)-1]
	dat, err1 := ioutil.ReadFile(fmt.Sprintf("data/%s", filename))
	if err1 != nil {
		fmt.Println("file reading error: %s", filename)
		return
	}
	w.Write(dat)
}

func main() {
	http.HandleFunc("/storage_upload/*", storageUploadHandler)
	http.HandleFunc("/storage_download/*", storageDownloadHandler)
	if err := http.ListenAndServe(addr, nil); err != nil {
		panic(err)
	}
}
