package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	crypto_rand "crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"github.com/avast/retry-go"
	"github.com/google/go-github/v62/github"
	"github.com/robfig/cron/v3"
	"golang.org/x/crypto/nacl/box"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"text/template"
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
var isLog = os.Getenv("BAK_LOG")
var branch = os.Getenv("BAK_BRANCH")
var delayRestore = os.Getenv("BAK_DELAY_RESTORE")
var tmpPath = os.TempDir()
var cronManager = cron.New(cron.WithSeconds())

const readmeTemplate = `# {{.Title}}

**上一次更新：{{.LastUpdate}}**

## 应用列表

| {{range .Table.Headers}}{{.}} | {{end}}
| {{range .Table.Headers}}---| {{end}}
{{range .Table.Rows}}| {{range .}}{{.}} | {{end}}
{{end}}
`
const clearHistoryWorkflowYml = `
name: Clear Git History
on:
  schedule:
    - cron: '10 22 * * *'
  workflow_dispatch:
jobs:
  clear-history:
    runs-on: ubuntu-latest
    steps:
      - name: Checkout code
        uses: actions/checkout@v4
        with:
          ref: ${{ github.head_ref }}
          fetch-depth: 0  # Fetch all history for all branches and tags
          token: ${{ secrets.PAT_TOKEN }}
      - name: Get default branch
        id: default_branch
        run: echo "::set-output name=branch::$(echo ${GITHUB_REF#refs/heads/})"
      - name: Remove git history
        env:
          DEFAULT_BRANCH: ${{ steps.default_branch.outputs.branch }}
        run: |
          git config --local user.email "github-actions[bot]@users.noreply.github.com"
          git config --local user.name "github-actions[bot]"
          git checkout --orphan tmp
          git add -A				# Add all files and commit them
          git commit -m "Reset all files"
          git branch -D $DEFAULT_BRANCH		# Deletes the default branch
          git branch -m $DEFAULT_BRANCH		# Rename the current branch to defaul
      - name: Push changes
        uses: ad-m/github-push-action@master
        with:
          force: true
          branch: ${{ github.ref }}
          github_token: ${{ secrets.PAT_TOKEN }}
`

func main() {
	if repo != "" && owner != "" && token != "" {
		LogEnv()
		if delayRestore != "" {
			//启动时延时还原数据
			delay, _ := strconv.Atoi(delayRestore)
			time.Sleep(time.Duration(delay) * time.Minute)
		}
		Restore()
		//定时备份
		CronTask()
		defer cronManager.Stop()
		select {}
	}

}
func LogEnv() {
	debugLog("BAK_REPO_OWNER：%s", owner)
	debugLog("BAK_REPO：%s", repo)
	debugLog("BAK_GITHUB_TOKEN：%s", "***********")
	debugLog("BAK_APP_NAME：%s", appName)
	debugLog("BAK_DATA_DIR：%s", dataDir)
	debugLog("BAK_PROXY：%s", proxy)
	debugLog("BAK_CRON：%s", SPEC)
	debugLog("BAK_MAX_COUNT：%s", maxCount)
	debugLog("BAK_LOG：%s", isLog)
	debugLog("BAK_BRANCH：%s", branch)
	debugLog("BAK_DELAY_RESTORE：%s", delayRestore)
	debugLog("TMP_PATH：%s", tmpPath)
}
func CronTask() {
	if SPEC == "" {
		SPEC = "0 0/10 * * * ?"
	}
	cronManager.AddFunc(SPEC, func() {
		retry.Do(
			func() error {
				return Backup()
			},
			retry.Delay(3*time.Second),
			retry.Attempts(3),
			retry.DelayType(retry.FixedDelay),
		)
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
		debugLog("Get Last Backup File: %s， Size: %d，Url: %s", content.GetPath(), content.GetSize(), content.GetDownloadURL())
		url := content.GetDownloadURL()
		//下载、解压文件
		zipFilePath := filepath.Join(tmpPath, *content.Name)
		DownloadFile(url, zipFilePath)
		debugLog("DownloadFile: %s", zipFilePath)
		Unzip(zipFilePath, dataDir)
		os.Remove(zipFilePath)
		debugLog("Unzip && Remove: %s", zipFilePath)
	}
}

func debugLog(str string, v ...any) {
	if isLog == "1" {
		if v != nil {
			log.Printf(str, v...)
		} else {
			log.Println(str)
		}
	}
}

func Backup() error {
	ctx := context.Background()
	chineseTimeStr(time.Now(), "200601021504")
	fileName := chineseTimeStr(time.Now(), "200601021504") + ".zip"
	zipFilePath := filepath.Join(tmpPath, fileName)
	debugLog("Start Zip File: %s", zipFilePath)
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
	if branch == "" {
		branch = "main"
	}
	commitMessage := "Add File"
	fileContent, _ := os.ReadFile(zipFilePath)
	client := github.NewClient(httpClient).WithAuthToken(token)
	err = AddOrUpdateFile(client, ctx, branch, appName+"/"+fileName, fileContent)
	if err != nil {
		return err
	}
	os.Remove(zipFilePath)
	//查询仓库中备份文件数量
	count, err := strconv.Atoi(maxCount)
	if err != nil {
		count = 5
	}
	_, dirContents, _, _ := client.Repositories.GetContents(ctx, owner, repo, appName, &github.RepositoryContentGetOptions{Ref: branch})
	commitMessage = "clean file"
	if len(dirContents) > count {
		for i, dc := range dirContents {
			if i+1 <= len(dirContents)-count {
				client.Repositories.DeleteFile(ctx, owner, repo, *dc.Path, &github.RepositoryContentFileOptions{
					Message: &commitMessage,
					SHA:     dc.SHA,
					Branch:  &branch,
				})
			}

		}

	}
	_, dirContents, _, _ = client.Repositories.GetContents(ctx, owner, repo, "", &github.RepositoryContentGetOptions{Ref: branch})
	rows := [][]string{}
	isFirstInit := true
	if len(dirContents) > 0 {
		i := 0
		for _, dc := range dirContents {
			if dc.GetName() == ".github" {
				isFirstInit = false
			}
			if dc.GetType() == "dir" && dc.GetName() != ".github" {
				commits, _, _ := client.Repositories.ListCommits(ctx, owner, repo, &github.CommitsListOptions{
					Path: dc.GetPath(),
					ListOptions: github.ListOptions{
						PerPage: 1,
					},
				})
				commitDate := commits[0].GetCommit().GetAuthor().GetDate()
				_, dcs, _, _ := client.Repositories.GetContents(ctx, owner, repo, dc.GetPath(), &github.RepositoryContentGetOptions{Ref: branch})
				row := []string{}
				i++
				row = append(row,
					fmt.Sprintf("%d", i),
					dc.GetName(),
					chineseTimeStr(commitDate.Time, "2006-01-02 15:04:05"),
					fmt.Sprintf("[%s](%s)", dcs[len(dcs)-1].GetName(), dcs[len(dcs)-1].GetDownloadURL()))
				rows = append(rows, row)
			}
		}
	}
	_, dirContents, _, _ = client.Repositories.GetContents(ctx, owner, repo, "", &github.RepositoryContentGetOptions{Ref: branch})
	if len(rows) > 0 {
		readmeContent := ReadmeData{
			Title:      repo,
			LastUpdate: chineseTimeStr(time.Now(), "2006-01-02 15:04:05"),
			Table: TableData{
				Headers: []string{"序号", "应用名称", "更新时间", "最近一次备份"},
				Rows:    rows,
			},
		}
		tmpl, _ := template.New("readme").Parse(readmeTemplate)
		var buf bytes.Buffer
		err = tmpl.Execute(&buf, readmeContent)
		if err != nil {
			panic(err)
		}
		readmeStr := buf.String()
		debugLog(readmeStr)
		_ = AddOrUpdateFile(client, ctx, branch, "README.md", []byte(readmeStr))
	}
	if isFirstInit {
		_ = AddOrUpdateFile(client, ctx, branch, ".github/workflows/clear-history.yml", []byte(clearHistoryWorkflowYml))
		input := &github.DefaultWorkflowPermissionRepository{
			DefaultWorkflowPermissions: github.String("write"),
		}
		_, _, _ = client.Repositories.EditDefaultWorkflowPermissions(ctx, owner, repo, *input)
		_ = addRepoSecret(ctx, client, owner, repo, "PAT_TOKEN", token)
	}
	return nil
}
func addRepoSecret(ctx context.Context, client *github.Client, owner string, repo, secretName string, secretValue string) error {
	publicKey, _, err := client.Actions.GetRepoPublicKey(ctx, owner, repo)
	if err != nil {
		return err
	}

	encryptedSecret, err := encryptSecretWithPublicKey(publicKey, secretName, secretValue)
	if err != nil {
		return err
	}

	if _, err := client.Actions.CreateOrUpdateRepoSecret(ctx, owner, repo, encryptedSecret); err != nil {
		return fmt.Errorf("Actions.CreateOrUpdateRepoSecret returned error: %v", err)
	}

	return nil
}

func encryptSecretWithPublicKey(publicKey *github.PublicKey, secretName string, secretValue string) (*github.EncryptedSecret, error) {
	decodedPublicKey, err := base64.StdEncoding.DecodeString(publicKey.GetKey())
	if err != nil {
		return nil, fmt.Errorf("base64.StdEncoding.DecodeString was unable to decode public key: %v", err)
	}

	var boxKey [32]byte
	copy(boxKey[:], decodedPublicKey)
	encryptedBytes, err := box.SealAnonymous([]byte{}, []byte(secretValue), &boxKey, crypto_rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("box.SealAnonymous failed with error %w", err)
	}

	encryptedString := base64.StdEncoding.EncodeToString(encryptedBytes)
	keyID := publicKey.GetKeyID()
	encryptedSecret := &github.EncryptedSecret{
		Name:           secretName,
		KeyID:          keyID,
		EncryptedValue: encryptedString,
	}
	return encryptedSecret, nil
}
func AddOrUpdateFile(client *github.Client, ctx context.Context, branch, filePath string, fileContent []byte) error {
	newFile := false
	fc, _, _, err := client.Repositories.GetContents(ctx, owner, repo, filePath, &github.RepositoryContentGetOptions{Ref: branch})
	if err != nil {
		responseErr, ok := err.(*github.ErrorResponse)
		if !ok || responseErr.Response.StatusCode != 404 {
			newFile = false
		} else {
			newFile = true
		}
	}
	currentSHA := ""
	commitMessage := fmt.Sprintf("Add file: %s", filePath)
	if !newFile {
		currentSHA = *fc.SHA
		commitMessage = fmt.Sprintf("Update file: %s", filePath)
		_, _, err = client.Repositories.UpdateFile(ctx, owner, repo, filePath, &github.RepositoryContentFileOptions{
			Message: &commitMessage,
			SHA:     &currentSHA,
			Content: fileContent,
			Branch:  &branch,
		})
	} else {
		_, _, err = client.Repositories.CreateFile(ctx, owner, repo, filePath, &github.RepositoryContentFileOptions{
			Message: &commitMessage,
			Content: fileContent,
			Branch:  &branch,
		})
	}
	if err != nil {
		log.Println(err)
	}
	return err
}

func DownloadFile(downUrl, filePath string) {

	tr := &http.Transport{TLSClientConfig: &tls.Config{
		InsecureSkipVerify: true,
	}}
	if proxy != "" {
		proxyUrl, err := url.Parse(proxy)
		if err == nil {
			tr.Proxy = http.ProxyURL(proxyUrl)
		}
	}

	// 创建一个带有自定义 Transport 的 Client
	client := &http.Client{Transport: tr}

	req, err := http.NewRequest(http.MethodGet, downUrl, nil)
	if err != nil {
		fmt.Println(err)
	}
	r, err := client.Do(req)
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
		relPath, _ := filepath.Rel(src_dir, path)
		header.Name = filepath.ToSlash(relPath)

		// 判断：文件是不是文件夹
		if info.IsDir() {
			header.Name += "/"
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

type TableData struct {
	Headers []string
	Rows    [][]string
}

type ReadmeData struct {
	Title      string
	LastUpdate string
	Table      TableData
}

func chineseTimeStr(t time.Time, layout string) string {
	loc := time.FixedZone("UTC+8", 8*60*60)
	currentTime := t.In(loc)
	formattedTime := currentTime.Format(layout)
	return formattedTime
}
