# Kubernetes WordPress Operator

## 临时启动 K3S 集群

```sh
❯ minikube start \
  --nodes=3 \
  --driver=docker \
  --container-runtime=containerd \
  --kubernetes-version=v1.34.0 \
  --cpus=2 \
  --memory=2200mb \
  --disk-size=20g \
  --image-mirror-country='cn' \
  --image-repository=registry.cn-hangzhou.aliyuncs.com/google_containers \
  --registry-mirror=https://docker.lms.run,https://docker-0.unsee.tech,https://docker.m.daocloud.io \
  --cni=flannel \
  --extra-config=kubelet.housekeeping-interval=10s

❯ minikube kubectl -- get node -o wide
NAME           STATUS   ROLES           AGE     VERSION   INTERNAL-IP    EXTERNAL-IP   OS-IMAGE                         KERNEL-VERSION     CONTAINER-RUNTIME
minikube       Ready    control-plane   2m32s   v1.34.0   192.168.49.2   <none>        Debian GNU/Linux 12 (bookworm)   6.12.67-linuxkit   containerd://2.2.1
minikube-m02   Ready    <none>          2m21s   v1.34.0   192.168.49.3   <none>        Debian GNU/Linux 12 (bookworm)   6.12.67-linuxkit   containerd://2.2.1
minikube-m03   Ready    <none>          2m15s   v1.34.0   192.168.49.4   <none>        Debian GNU/Linux 12 (bookworm)   6.12.67-linuxkit   containerd://2.2.1
```

一个基于 [kubebuilder v4](https://book.kubebuilder.io/) + [controller-runtime](https://github.com/kubernetes-sigs/controller-runtime) 编写的 Kubernetes Operator，
用单个 `WordPress` 自定义资源声明式管理 WordPress 网站，覆盖**内置 MySQL** 与**外置数据库**两类常见生产场景。

## 功能特性

- **一键部署**：一个 `WordPress` CR → 自动生成 Deployment、Service、PVC、Secret，按需生成 StatefulSet (内置 MySQL)、Ingress、HPA
- **数据库灵活**
  - `database.mode: Internal` — Operator 自动创建 MySQL StatefulSet（含 PVC、Headless Service、自动生成的强随机密码 Secret）
  - `database.mode: External` — 直连外部 MySQL/MariaDB，密码通过用户指定 Secret 引用
- **生产可用配置项**：副本数、镜像、Pull Secret、资源 requests/limits、NodeSelector、Tolerations、Affinity、PodLabel/Annotation、滚动更新参数
- **可选服务暴露**：ClusterIP / NodePort / LoadBalancer，可启用 Ingress（Host、IngressClass、TLS 与注解）
- **可选弹性伸缩**：HPA（CPU 利用率），开启时 Operator 自动放手 Deployment.Replicas，避免冲突
- **健壮的状态机**：`Phase`（Pending / Provisioning / Ready / Failed）+ 标准 `Conditions`（`Ready`、`DatabaseReady`、`DeploymentReady`、`IngressConfigured`）+ `URL`、`ReadyReplicas`、`ObservedGeneration`
- **冲突自愈**：状态写入 `RetryOnConflict`，对 update conflict 静默重试，日志干净
- **级联删除**：所有由 Operator 创建的资源全部带 OwnerReference，删除 CR 即自动 GC
- **kubectl 友好**：`kubectl get wp` 列出 `Phase / Ready / Replicas / URL / Age`；支持 `kubectl scale wp/<name> --replicas=N`（通过 `subresource:scale`）

## 目录结构

```sh
api/v1alpha1/wordpress_types.go        # CRD 类型定义（含 +kubebuilder marker）
internal/controller/
  wordpress_controller.go              # 主 Reconcile 流程
  defaults.go                          # in-code 默认值
  database.go                          # 内置 MySQL StatefulSet/Secret/Service 构造
  resources.go                         # WordPress Deployment/Service/PVC/Ingress/HPA 构造
  names.go                             # 命名约定与 label helpers
  wordpress_unit_test.go               # fake-client 单元测试
  suite_test.go / wordpress_controller_test.go   # envtest 集成测试（缺二进制时自动 skip）
config/crd/bases/                      # 生成的 CRD
config/rbac/                           # 生成的 RBAC（含三个 wordpress-{admin,editor,viewer}-role）
config/samples/                        # 三份示例 CR
cmd/main.go                            # Manager 入口
```

## 快速开始

需要 Go ≥ 1.24、Docker、kubectl，以及一个可访问的 K8s 集群（例如 minikube）。

### 1. 安装 CRD

```bash
make manifests          # 重新生成 CRD（如修改了 api/）
kubectl apply -f config/crd/bases/apps.kubesphere.ai_wordpresses.yaml
```

或一次到位：`make install`

### 2. 在本机以 out-of-cluster 模式运行 Operator

```bash
GOSUMDB=off go run ./cmd/main.go --metrics-bind-address=0 --health-probe-bind-address=:8082 --leader-elect=false
```

或者使用 kubebuilder 默认目标：`make run`

### 3. 部署一个内置 MySQL 的 WordPress

```bash
kubectl create namespace wp-demo
kubectl -n wp-demo apply -f config/samples/apps_v1alpha1_wordpress.yaml
kubectl -n wp-demo get wp -w
# NAME              PHASE   READY   REPLICAS   URL   AGE
# wordpress-sample  Ready   1       1                2m
```

通过端口转发本地访问：

```bash
kubectl -n wp-demo port-forward svc/wordpress-sample 8080:80
# 浏览器打开 http://localhost:8080 → 跳转到 /wp-admin/install.php
```

### 4. 部署一个使用外置 MySQL 的 WordPress

先准备外置 DB 与密码 Secret（示例）：

```bash
kubectl create secret generic wpext-db -n wp-ext --from-literal=password='your-strong-password'
kubectl apply -f config/samples/apps_v1alpha1_wordpress_external_db.yaml
```

`spec.database.external` 中的 `host` / `port` / `passwordSecret` 指向你已有的 MySQL/MariaDB。
Operator 不会再创建任何数据库资源，只生成 WordPress Deployment、Service、PVC 等。

### 5. 部署集成 Ingress + HPA + LoadBalancer 的完整版

```bash
kubectl apply -f config/samples/apps_v1alpha1_wordpress_internal_full.yaml
```

## CRD 字段速览（`spec`）

| 字段 | 类型 | 默认 | 说明 |
| --- | --- | --- | --- |
| `image` | string | `wordpress:6.5-apache` | WordPress 容器镜像 |
| `replicas` | int32 | `1` | 副本数（开启 autoscaling 时由 HPA 接管） |
| `siteURL` | string | — | 写入 `WORDPRESS_HOME / WORDPRESS_SITEURL` |
| `service.type` | enum | `ClusterIP` | `ClusterIP` / `NodePort` / `LoadBalancer` |
| `service.port` | int32 | `80` | Service 暴露端口 |
| `persistence.enabled` | bool | `true` | 是否使用 PVC 挂载 `/var/www/html` |
| `persistence.size` | string | `5Gi` | PVC 容量 |
| `ingress.enabled` | bool | `false` | 启用 Ingress（需提供 `host`） |
| `ingress.host` / `ingressClassName` / `tlsSecretName` / `annotations` | — | — | Ingress 详细配置 |
| `autoscaling.{enabled,min,max,target}` | — | — | HPA 配置 |
| `database.mode` | enum | `Internal` | `Internal` / `External` |
| `database.{name,user,tablePrefix}` | string | `wordpress` / `wordpress` / `wp_` | 数据库 / 用户 / 表前缀 |
| `database.internal.{image,storageSize,storageClassName,resources,rootPasswordSecret,...}` | — | — | 内置 MySQL 详细配置 |
| `database.external.{host,port,passwordSecret}` | — | port=3306 | 外置 DB 连接信息 |

完整字段见 `config/crd/bases/apps.kubesphere.ai_wordpresses.yaml`（由 controller-gen 从 Go 类型生成）。

## 状态字段

```yaml
status:
  phase: Ready                     # Pending / Provisioning / Ready / Failed
  url: https://blog.example.com    # siteURL 优先；否则取 ingress
  replicas: 2
  readyReplicas: 2
  databaseHost: blog-mysql.demo.svc
  observedGeneration: 1
  conditions:
    - type: Ready              # 综合就绪
    - type: DatabaseReady      # 内置 MySQL 已 Ready 或外置 Secret 已找到
    - type: DeploymentReady    # WordPress Deployment 全副本就绪
    - type: IngressConfigured  # Ingress 已 reconcile（关闭时自动移除）
```

## 测试

### 单元测试（fake client，无依赖）

```bash
GOSUMDB=off go test ./internal/controller/... -count=1
# ok  github.com/.../internal/controller   ~0.1s
```

覆盖：

- `TestApplyDefaults` — 默认值注入
- `TestValidateSpec` — 5 个用例（含 external 缺失、ingress 缺 host、autoscaling min>max）
- `TestReconcileInternalCreatesAllResources` — 内置场景全资源创建
- `TestReconcileExternalDatabaseRequiresSecret` — 缺失外部 Secret 时设置正确条件
- `TestReconcileExternalDatabaseHappyPath` — 外置场景不创建 MySQL，env 引用用户 Secret
- `TestReconcileIngressAndHPALifecycle` — Ingress / HPA 启停的创建-清理循环

### 集成测试（envtest）

```bash
make test    # 自动下载 setup-envtest 二进制
```

### 端到端验证（minikube）

本仓库已在 `minikube v1.34` 上完整验证：

| 场景 | 结果 |
| --- | --- |
| 内置 MySQL：CRD → CR → MySQL StatefulSet/Deployment/PVC/Service/Secret | ✅ Phase=Ready，`curl wordpress-sample` 返回 200 + `/wp-admin/install.php` |
| 外置 MySQL：连接独立命名空间下的现有 MySQL | ✅ Phase=Ready，env 写入正确 host:port，未创建任何 MySQL 资源 |
| Ingress 启 / 禁 | ✅ 启用即创建，禁用立即删除 |
| HPA 启 / 禁 | ✅ 启用后接管副本，禁用后 Operator 重新接管 spec.replicas |
| 注入错误 Secret 名 | ✅ `DatabaseReady=False, Reason=ExternalSecretMissing`，恢复后自动回到 Ready |
| 删除 CR | ✅ 全部从属资源被 OwnerReference 级联回收（用户自带 Secret 不动） |

## 构建容器镜像并部署到集群

```bash
make docker-build IMG=ghcr.io/you/wordpress-operator:0.1.0
make docker-push  IMG=ghcr.io/you/wordpress-operator:0.1.0
make deploy       IMG=ghcr.io/you/wordpress-operator:0.1.0
```

卸载：`make undeploy`

## 设计要点

1. **资源构造与 Reconcile 分层**：`resources.go`/`database.go` 为纯函数构造器，便于单元测试；`wordpress_controller.go` 只负责调度与 API 交互。
2. **Owner Reference 全覆盖**：所有由 Operator 创建的对象均 `controllerutil.SetControllerReference`，使 `kubectl delete wp` 即可级联清理。
3. **Secret 不滚动**：内置 MySQL 的随机密码只在第一次创建时生成；后续 reconcile 使用 Get-or-Skip，避免误覆盖造成数据库登录失败。
4. **HPA 与 Operator 不打架**：`autoscaling.enabled=true` 时 Deployment 不写 `spec.replicas`，HPA 独占控制；关闭时 Operator 立即重新写入。
5. **状态写入用 RetryOnConflict**：先 Get 最新，再覆盖 status，再 Update；冲突自动重试。
6. **Conflict 自愈**：reconcile 内部对子资源更新冲突 (`apierrors.IsConflict`) 静默吞掉，等待下一次 watch 事件触发，避免日志噪声。
7. **CRD 默认值与代码默认值并行**：CRD 上的 `+kubebuilder:default` 经 apiserver 注入；代码内 `applyDefaults()` 兜底，保证 fake-client 与本地默认行为一致。

## 许可证

Apache 2.0
