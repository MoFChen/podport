# PodPort 3 · Go 个人版

用一张表记录 Podman 的自定义网络、Pod、容器、多程序监听和主机端口映射。全部手动维护，没有账号、多用户、后台同步或 Podman API 依赖。

## 最简单的运行方式

从多平台包中取出对应平台的程序，重命名为 `podport`（Windows 为 `podport.exe`），放到可写目录后运行：

```bash
./podport
```

打开 **http://127.0.0.1:8090**。网页、SVG 图标和 SQLite 引擎已编译进程序，不需要 Node、Python、系统 SQLite 或额外前端文件。

程序在**自身所在目录**创建 `podport.db`，与启动时的工作目录无关。平时只需一个二进制和一个数据库文件。SQLite 使用 DELETE 日志模式；写事务期间可能短暂产生 `podport.db-journal`，提交后自动删除，不常驻 WAL/SHM。

```bash
./podport -listen 127.0.0.1:8090 -db ./my-ports.db
./podport -version
```

默认仅监听本机，程序目录不可写时可用 `-db` 指定可写位置。

## 怎么登记

一次添加一个容器，填写主机、可选 Pod 与自定义网络，再添加它的所有监听端口。

- **自定义网络**：填写名称即可登记，多个网络用逗号分隔。现有网络可点选；同一主机的同名网络视为同一网络。Pod 网络列表由成员共享，保存任一成员的网络修改会在同一事务中同步到其他成员。独立容器可加入同一网络，也可加入多个网络。
- **内部监听**：一个容器可以添加多条程序监听，例如 Emby 的 8096、8920。每条监听可选择 TCP、UDP 或 TCP + UDP，并分别设置 IP、用途、备注和状态。
- **对外映射**：每条监听可添加多个主机映射，主机 IP、端口、协议和状态分别配置；留空即仅登记内部监听。映射协议必须是监听协议的子集，例如监听 TCP + UDP、映射只发布 TCP。
- **双协议**：Syncthing 的 22000 可以只登记一条 TCP + UDP；冲突检查会分别检查两种协议。
- **预留与释放**：预留端口仍占用登记空间；释放映射只释放主机端口，内部监听仍占用。释放内部监听后，其下映射也不再占用，但记录保留。
- **查询**：搜索任意监听端口、映射端口、容器、Pod、网络或用途；数字搜索也命中端口范围。按主机、网络或监听状态筛选。列表以容器为单位显示其全部端口，方便一起编辑。
- **外观与拓扑**：默认跟随系统，可切换浅色/深色并记住选择。常规字号 18px，2200px 以上视口为 22px，适合 4K 屏幕；仍支持浏览器缩放。拓扑使用固定视框的 SVG 图标，显示自定义网络 → Pod/容器 → 内部监听 → 主机发布，可拖动、缩放、草稿预览、冲突标红并导出当前主题 SVG。

### nginx 反向代理 Emby 的登记示例

| 主机 | Pod | 容器 | 自定义网络 | 内部监听 | 主机发布 |
|---|---|---|---|---|---|
| nas | web | nginx | media-net | 80/TCP、443/TCP+UDP | 8080→80、8443→443 |
| nas | 留空 | emby | media-net | 8096/TCP、8920/TCP | 无 |

拓扑中 `media-net` 同时连接 Pod `web` 和独立容器 `emby`。在实际 Podman 网络开启 DNS、名称可解析且 Emby 监听可访问地址时，nginx 可将上游配置为 `http://emby:8096`，无需先发布 Emby 的主机端口。应用只保存这些登记关系，不创建实际网络、不检测连通性，也不生成或修改 nginx 配置。

同一自定义桥接网络不等于同一网络命名空间：不同 Pod/独立容器可以重复使用内部端口；同一 Pod 内共享网络的容器则不能在相同 IP/协议上重复监听同一端口。主机映射按同一实际主机、绑定 IP、协议和端口范围检查；`0.0.0.0` 覆盖全部 IPv4 绑定。网络名不改变主机端口冲突规则。

支持 IPv4、TCP、UDP、1–65535 端口范围。每个容器最多 128 条监听、每条监听最多 64 个映射、每个命名空间最多 32 个网络，总计最多 5000 个容器、20000 条监听与映射。未登记不代表操作系统实际空闲；同一实际主机应始终使用同一个主机名。

网络语义参考 [Podman pod create](https://docs.podman.io/en/latest/markdown/podman-pod-create.1.html) 与 [Podman network connect](https://docs.podman.io/en/latest/markdown/podman-network-connect.1.html)。

## Podman 运行与内存限制

交付 Linux/amd64 OCI 镜像，也可从源码构建对应平台的镜像：

```bash
podman load -i podport-3.0.0-linux-amd64.oci.tar
# 或：podman build -t localhost/podport:3.0.0 .

podman volume create podport-data
podman run -d --name podport \
  -p 127.0.0.1:8090:8080 \
  -v podport-data:/data:Z \
  --memory=64m --memory-swap=64m \
  -e GOMEMLIMIT=48MiB \
  --read-only --cap-drop=ALL \
  --security-opt=no-new-privileges \
  localhost/podport:3.0.0
```

镜像从 `scratch` 开始，只含静态 Go 程序、空数据目录和许可证；默认 UID/GID 65532，无 shell 或包管理器。数据库位于 `/data/podport.db`，挂载整个目录可供 SQLite 创建短暂事务日志。

`--memory=64m` 为容器硬限制，需要主机 cgroup 支持；`GOMEMLIMIT=48MiB` 为 Go 运行时软目标，无法限制进程全部内存。程序未设置此环境变量时也默认采用 48 MiB 软目标。大批量数据和并发请求仍需按实测调整硬限制。

验证范围与实测数据见 `VERIFICATION.md`。目标主机可运行 `python3 scripts/verify-image.py --skip-build` 检查镜像、64 MiB 限制、网络/端口规则和持久化。Python 仅为可选验收脚本所需，应用运行不需要它。

## 备份与旧版本升级

右上角“•••”可导出/导入 JSON、下载 SQLite 快照。导入会先校验全部数据，确认时下载当前 JSON 备份，再原子替换登记。正常退出后直接复制 `podport.db` 可迁移到其他平台；运行期间应使用网页快照，避免复制正在写入的数据库。

v3 支持导入 v1/v2/v3 JSON，也可停止旧版后直接使用旧数据库：

```bash
./podport -db /旧数据目录/podport.db
```

升级前保留一份数据库副本。首次打开时，旧版平铺记录按“主机 + 容器”合并为多个监听及映射，保留端口状态、备注和已释放映射；旧版没有的自定义网络初始为空。原表保留，新数据写入 `registry_containers`，迁移成功后只执行一次。原版程序不会显示 v3 之后的修改；v3 删除数据后也不会从旧表重新恢复。

SQLite 表直接保存容器基础字段及 `networks_json`、`listeners_json` 两个合法 JSON 列，不需要额外的状态或配置文件。若旧数据中同一主机有多个同名容器却属于不同 Pod/模式，升级会报错并保留原表，请先在旧版修正。

## 从源码构建

需要 Go 1.27+，没有前端构建步骤：

```bash
CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags='-s -w' -o podport .
go test ./...
# 开发机有 C 编译器时可检查竞态：
go test -race ./...
```

`./scripts/build.sh` 编译 Linux、macOS、Windows 的 amd64/arm64 六个平台程序。每个平台使用对应的二进制；Linux 产物无动态解释器，SQLite 使用 `modernc.org/sqlite`，不需要 CGo。

`npm ci && npm test` 执行前端 DOM 与规则测试，Node 仅供开发。正式 `web/config.js` 使用 Go API；在线预览独立生成会话演示配置，不修改生产网页配置，刷新预览会恢复示例。

许可证 MIT；依赖许可证见 `THIRD_PARTY_NOTICES.txt`。
