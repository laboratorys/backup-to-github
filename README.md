## backup-to-github
### 特性
1. 主要是针对一些云容器在重启后，数据会丢失的低成本解决方案，尤其是很多基于sqlite的应用。
2. 适用范围：数据实时性要求没那么高的场景。
3. 定时备份数据到GitHub仓库
4. 容器重启时还原最近一次数据备份。
### 环境变量
| 变量名               | 是否必填 | 说明                          | 示例                     |
|-------------------|------|-----------------------------|------------------------|
| BAK_APP_NAME      | 是    | 备份应用名称，用于区分不同应用的备份数据        | uptime                 |
| BAK_DATA_DIR      | 是    | 计划备份的应用程序数据目录               | /app/data              |
| BAK_GITHUB_TOKEN  | 是    | 备份github账号的`access_token`   |                        |
| BAK_PROXY         | 否    | 备份代理，无网络问题无需设置此项            | http://localhost:10809 |
| BAK_REPO          | 是    | 备份仓库名称                      | xxx_repo               |
| BAK_REPO_OWNER    | 是    | 备份仓库拥有者                     | xxx                    |
| BAK_CRON          | 否    | 定时备份数据，默认值：  0 0/10 * * * ? |                        |
| BAK_MAX_COUNT     | 否    | 备份文件在仓库中保留的最大数量，默认：30       | 30                     |
| BAK_LOG           | 否    | 开启日志，用于调试                   | 1                      |
| BAK_BRANCH        | 否    | 备份仓库对应分支，默认：main            | main                   |
| BAK_DELAY_RESTORE | 否    | 还原延迟，容器启动后延迟还原data          |                        |
### 使用
以Uptime Kuma的Dockerfile作为示例
```
FROM alpine AS builder
RUN apk add --no-cache nodejs npm git curl tar libc6-compat

RUN npm install npm -g

RUN adduser -D app
USER app
WORKDIR /home/app

ARG BAK_VERSION=1.7
ENV BAK_VERSION=${BAK_VERSION}
RUN curl -L "https://github.com/laboratorys/backup-to-github/releases/download/v${BAK_VERSION}/backup2gh-v${BAK_VERSION}-linux-amd64.tar.gz" -o /tmp/backup-to-github.tar.gz \
    && cd $WORKDIR && tar -xzf /tmp/backup-to-github.tar.gz \
    && rm /tmp/backup-to-github.tar.gz


RUN git clone https://github.com/louislam/uptime-kuma.git
WORKDIR /home/app/uptime-kuma
RUN npm run setup

EXPOSE 3001
CMD ["sh", "-c", "nohup /home/app/backup2gh & node server/server.js"]
```
1. 为确保alpine镜像可以顺利执行`backup2gh`， 需要安装依赖`curl tar libc6-compat`，ubuntu等镜像不需要额外安装`libc6-compat`
2. `ARG BAK_VERSION=xx`设置备份程序版本，建议使用最新版。`RUN curl -L...`照抄即可
3. CMD命令将备份程序执行在前，也可以使用`ENTRYPOINT`
4. 单仓库多应用时，定时执行的时间尽量错开，避免SHA变更导致的备份失败。
5. 大部分情况下，备份频率不用很高、备份文件不用保留很多。
