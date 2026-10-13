# Docker 归组与真实 Runner 基线

范围：用户要求后续 Docker 容器集中管理，作为 #5／#7 后续沙箱执行的前提修复。
管理目录为 `deployments/docker/`，固定 Compose project `localcode`。
本轮不迁移或删除旧容器、volume、镜像、Ledger 或任务输入，也不修改前端布局。

## 实现与拒绝边界

固定 OpenSandbox 上游仍为 `f89eff24bbd15ae1c37a47769f7667849e679be0`。
server 配方和补丁由可信服务设置 task／egress 分组及 owner/purpose；客户端 metadata
仍只有 operation ID 与 policy SHA。Go Ready／Run 先核对控制服务的 project/service
以及镜像内分组能力标记，执行前复验 task／egress；缺失或错组时停止执行。
原物理 ID、operation、policy 和重绑拒绝等停止约束保持，标签本身不授权删除。
普通 Docker／WSL Docker 后端共用的启动参数也固定 `localcode` project、`sandbox`
service、owner/purpose；未新增用户参数或改变精确生命周期。现有公开命令测试先
因缺失 project 标签 RED exit 1，加入标签后 Docker／WSL 命令回归 GREEN exit 0。

公开 OpenSandbox Runner 接缝的四个新增 RED 都证明旧实现实际派发了命令：未分组控制服务、
只补标签的旧镜像、未分组 task、其他 project 的 egress。GREEN 分别证明前两项
创建前拒绝，后两项执行前拒绝并终止精确本次资源。原始 RED、GREEN 和失败分母保留。

## 新环境与真实基线

从已核验的固定 Git blob 复制上游 server，重新应用公开 patch；控制依赖继续复用
原冻结、逐项 hash 核验的 requirements、49 wheel 与许可证。构建均
`docker build --pull=false --network=none`，不是在线重新选取依赖。
新构建、Compose 配置、manifest 和审计都位于管理目录的忽略 `.local/` 内。
启动的两个新控制服务为 `localcode-opensandbox-go-1`、`localcode-opensandbox-python-1`；
旧资源和既有只读 Task Workspace 根保留。

| 固定输入 | 身份 |
| --- | --- |
| Go server image | `sha256:ca96deb69878ffb6f14f4f072f5df74c4eb124be631086027546de21ece5ae16` |
| Go policy | `6935d7357805ed185b277e9470c08a48e07aa16009b777ec12d4736fbb6e1861` |
| Python server image | `sha256:bb120a31bbc000561e190d2307bcf376485b937b5adc14cd6906ab591e576fbb` |
| Python policy | `f4d9e809518f0a2d631d7d2eabc782a8bb8c6acb163ef7bd35ce72d5c38e9f30` |

原 task、execd、egress pin 与已批准 pytz 保持。实际公开 Runner.Ready／Run 验证：

- Go：冻结 chi 的完整 `go test ./... -count=1`，两个包通过，6.946 秒／26.362 秒，
  退出 0，输出未截断；98 个来源文件加 1 个 lease Git marker 前后 hash 相同。
- Python：冻结 schedule 的全部 `python -m unittest discover -v`，先在隔离 scratch
  从唯一只读 pytz wheel 离线安装，81 passed／0 failed／0 skipped，退出 0，输出未截断；
  32 个来源文件加 1 个 lease Git marker 前后 hash 相同。
- 两轮都直接观察实际 task／egress 的 `localcode` project、对应 service、owner、purpose、
  operation、policy、只读 mount 和资源限制；随后精确两个物理 ID 均不存在，Runner Stopped 为 true。

首次驱动预检查把来源文件数当成全部输入数，漏计 lease Git marker，创建 task 前 exit 1；
修正后保留全部 99／33 输入并完成两轮测试。没有删文件、跳测试或缩减上游分母。

## 验证范围与剩余工作

最终新鲜命令：

| 验证 | 完整结果 |
| --- | --- |
| 四条新增 Runner 拒绝场景 | 初次 RED 均 exit 1；修复后两组 GREEN 均 exit 0 |
| 现有 Runner 定向回归 | `go test ./internal/sandbox -run 'TestOpenSandboxRunner\|TestRoutingRunnerKeepsExplicitOpenSandbox' -count=1`，exit 0 |
| 完整 Go | `go test ./... -count=1 -json`，1520 pass／72 skip／0 fail test actions；45 包 pass、13 无测试包，exit 0 |
| Go 静态检查 | `go vet ./...`，exit 0 |
| 完整 Python | `python -m pytest -q`，2483 passed／17 skipped／0 failed、3 warnings，615.09 秒，exit 0 |
| 真实公共 Runner 基线 | Go 2/2 包、Python 81/81；两轮 exit 0、0 skip／fail，实际标签与输入核对通过 |
| 差异检查 | `git diff --check`，exit 0 |

以上首次完整回归与基线原结果保留。末轮发现普通 Docker／WSL 共用 assembler 缺口后，
补齐标签并重新编译诊断入口，重跑最终完整回归和两条真实基线：Go 仍为
1520 pass／72 skip／0 fail、45 包 pass／13 无测试包；Python 仍为
2483 passed／17 skipped／0 failed、3 warnings，564.97 秒；vet 与差异检查 exit 0。
最终真实 Go 两个包为 7.946 秒／26.269 秒，Python 81 项为 0.046 秒，两轮 exit 0，
完整输入和物理终止核对相同。双轴源码与补充调用方审阅均 `AI_REVIEWED`，
Standards 0、Spec 0 未解决发现；不宣称 `HUMAN_REVIEWED`。
单元、集成与上述真实 OpenSandbox Runner E2E 分别记录；
普通 Docker／WSL 的当前证明范围是命令与源码回归，未宣称本轮 WSL VM 运行验证。
模型调用、本轮浏览器产品验证、Trace 后端读回及完整发布未运行；这两条真实基线
不替代 #5／#7 或已关闭基线票当时的产品范围证据，也不产生人工复核结论。
后续模型产品进程须使用本轮新分组 manifest 和对应进程凭据，旧 manifest 不作为新执行前提。

不可变本机收据位于 `deployments/docker/.local/receipts/grouping-20261013/receipt.json`，
SHA-256 为 `aee3a0ea85231dbf548068df50cc570cfc136d57269d5183933ef21a119d642c`；
27 项运行源码／模块／工作约定指纹为
`2849b513df8e8f5455aa6e9adadee4d5ce249d4129bcb634a780ac7cdfd4950c`。
收据记录实际输入、原失败与最终命令、原二进制与新二进制、实际镜像／策略和物理资源。
原 53 个可见容器全部保留，仅新增两个分组控制服务；canonical Ledger 前后摘要相同，
本轮模型调用 0，未结算、释放、重置或替换原 token 批次。
