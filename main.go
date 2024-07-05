package main

import (
	"archive/zip"
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"github.com/google/go-github/v62/github"
	"github.com/robfig/cron/v3"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var owner = os.Getenv("BAK_REPO_OWNER")
var repo = os.Getenv("BAK_REPO")
var token = os.Getenv("BAK_GITHUB_TOKEN")
var appName = os.Getenv("BAK_APP_NAME")
var dataDir = os.Getenv("BAK_DATA_DIR")
var proxy = os.Getenv("BAK_PROXY")
var SPEC = os.Getenv("BAK_CRON")
var maxCount = os.Getenv("BAK_MAX_COUNT")
var tmpPath = os.TempDir()
var cronManager = cron.New(cron.WithSeconds())

func main() {
	//启动时自动还原数据
	Restore()
	//定时备份
	CronTask()
	go func() {
		for {
			time.Sleep(time.Second)
		}
	}()
	select {}
}
func CronTask() {
	if SPEC == "" {
		SPEC = "* */10 * * * *"
	}
	cronManager.AddFunc(SPEC, func() {
		Backup()
	})
	cronManager.Start()
}
func Restore() {
	ctx := context.Background()
	proxyURL, err := url.Parse(proxy)
	if err != nil {
		log.Fatalf("Failed to parse proxy URL: %v", err)
	}
	transport := &http.Transport{
		Proxy: http.ProxyURL(proxyURL),
	}

	// 创建带有代理的 HTTP 客户端
	httpClient := &http.Client{
		Transport: transport,
	}
	if proxy == "" {
		httpClient = nil
	}
	client := github.NewClient(httpClient).WithAuthToken(token)
	_, dirContents, _, _ := client.Repositories.GetContents(ctx, owner, repo, appName, nil)
	if len(dirContents) > 0 {
		//取最后一个文件
		content := dirContents[len(dirContents)-1]
		url := content.GetDownloadURL()
		//下载、解压文件
		zipFilePath := filepath.Join(tmpPath, *content.Name)
		DownloadFile(url, zipFilePath)
		Unzip(zipFilePath, dataDir)
		os.Remove(zipFilePath)
	}
}

func Backup() {
	ctx := context.Background()
	fileName := time.Now().Format("200601021504" + ".zip")
	zipFilePath := filepath.Join(tmpPath, fileName)
	Zip(dataDir, zipFilePath)
	proxyURL, err := url.Parse(proxy)
	if err != nil {
		log.Fatalf("Failed to parse proxy URL: %v", err)
	}
	transport := &http.Transport{
		Proxy: http.ProxyURL(proxyURL),
	}
	// 创建带有代理的 HTTP 客户端
	httpClient := &http.Client{
		Transport: transport,
	}
	if proxy == "" {
		httpClient = nil
	}
	branch := "main"
	commitMessage := "Add File"
	fileContent, _ := os.ReadFile(zipFilePath)
	client := github.NewClient(httpClient).WithAuthToken(token)
	newFile := false
	fc, _, _, err := client.Repositories.GetContents(ctx, owner, repo, appName+"/"+fileName, &github.RepositoryContentGetOptions{Ref: branch})
	if err != nil {
		responseErr, ok := err.(*github.ErrorResponse)
		if !ok || responseErr.Response.StatusCode != 404 {
			newFile = false
		} else {
			newFile = true
		}
	}
	currentSHA := ""
	if !newFile {
		currentSHA = *fc.SHA
		commitMessage = "Update file"
		_, _, err = client.Repositories.UpdateFile(ctx, owner, repo, appName+"/"+fileName, &github.RepositoryContentFileOptions{
			Message: &commitMessage,
			SHA:     &currentSHA,
			Content: fileContent,
			Branch:  &branch,
		})
	} else {
		_, _, err = client.Repositories.CreateFile(ctx, owner, repo, appName+"/"+fileName, &github.RepositoryContentFileOptions{
			Message: &commitMessage,
			Content: fileContent,
			Branch:  &branch,
		})
	}
	if err != nil {
		log.Fatal(err)
	}
	os.Remove(zipFilePath)
	//查询仓库中备份文件数量
	count, err := strconv.Atoi(maxCount)
	if err != nil {
		count = 30
	}
	_, dirContents, _, _ := client.Repositories.GetContents(ctx, owner, repo, appName, nil)
	commitMessage = "clean file"
	if len(dirContents) > count {
		client.Repositories.DeleteFile(ctx, owner, repo, *dirContents[0].Path, &github.RepositoryContentFileOptions{
			Message: &commitMessage,
			SHA:     dirContents[0].SHA,
			Branch:  &branch,
		})
	}
}

func DownloadFile(downUrl, filePath string) {

	req, err := http.NewRequest(http.MethodGet, downUrl, nil)
	if err != nil {
		fmt.Println(err)
	}
	tr := &http.Transport{TLSClientConfig: &tls.Config{
		InsecureSkipVerify: true,
	}}

	proxyUrl, err := url.Parse(os.Getenv("BAK_PROXY"))
	if err == nil { // 使用传入代理
		tr.Proxy = http.ProxyURL(proxyUrl)
	}

	r, err := (&http.Client{Transport: tr}).Do(req)
	if err != nil {
		fmt.Println(err)
	}
	if r != nil {
		defer r.Body.Close()
	}

	// 获得get请求响应的reader对象
	reader := bufio.NewReaderSize(r.Body, 32*1024)
	file, err := os.Create(filePath)
	defer file.Close()
	if err != nil {
		panic(err)
	}
	// 获得文件的writer对象
	writer := bufio.NewWriter(file)

	io.Copy(writer, reader)
}

// 打包成zip文件
func Zip(src_dir string, zip_file_name string) {

	// 预防：旧文件无法覆盖
	os.RemoveAll(zip_file_name)

	// 创建：zip文件
	zipfile, _ := os.Create(zip_file_name)
	defer zipfile.Close()

	// 打开：zip文件
	archive := zip.NewWriter(zipfile)
	defer archive.Close()

	// 遍历路径信息
	filepath.Walk(src_dir, func(path string, info os.FileInfo, _ error) error {

		// 如果是源路径，提前进行下一个遍历
		if path == src_dir {
			return nil
		}

		// 获取：文件头信息
		header, _ := zip.FileInfoHeader(info)
		header.Name = strings.TrimPrefix(path, src_dir+`\`)

		// 判断：文件是不是文件夹
		if info.IsDir() {
			header.Name += `/`
		} else {
			// 设置：zip的文件压缩算法
			header.Method = zip.Deflate
		}

		// 创建：压缩包头部信息
		writer, _ := archive.CreateHeader(header)
		if !info.IsDir() {
			file, _ := os.Open(path)
			defer file.Close()
			io.Copy(writer, file)
		}
		return nil
	})
}

func Unzip(zipPath, dstDir string) error {
	// open zip file
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer reader.Close()
	for _, file := range reader.File {
		if err := unzipFile(file, dstDir); err != nil {
			return err
		}
	}
	return nil
}

func unzipFile(file *zip.File, dstDir string) error {
	// create the directory of file
	filePath := path.Join(dstDir, file.Name)
	if file.FileInfo().IsDir() {
		if err := os.MkdirAll(filePath, os.ModePerm); err != nil {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(filePath), os.ModePerm); err != nil {
		return err
	}

	// open the file
	rc, err := file.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	// create the file
	w, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer w.Close()

	// save the decompressed file content
	_, err = io.Copy(w, rc)
	return err
}
