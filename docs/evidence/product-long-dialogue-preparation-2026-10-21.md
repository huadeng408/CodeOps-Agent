# 独立长对话的便携构建与运行准备

范围为 [#27](https://github.com/huadeng408/CodeOps-Agent/issues/27) 的 v13/v7 准备，
运行源码 `befc2b0069128ea6652c70e99f5a89e2e2849303`。
旧尝试仍为 [180/200、未通过](product-long-dialogue-2026-10-21.md)，不与新尝试拼接。
用户批准后的 [831… 模型准入封存](canceled-file-read-admission-2026-10-21.md#用户批准后的实际封存)
已完成，原失败、unknown 与计量保留。

## 本轮已验证的准备范围

- 官方入口 `python -B scripts/build-windows.py --output <new-directory> --cache <existing-cache>`
  退出 0，用时 112.625 秒；在新目录构建，保留全部旧包。事先校验 61 个缓存文件，
  Python 使用 `PIP_NO_INDEX=1`、既有 wheel 缓存及锁文件，Go 使用缓存工具链与离线模块设置。
  没有变更依赖或启动产品／模型。
- 逐一核验 621 个构建源码与 3225 个运行文件；目录文件集合与 manifest 完全一致。
  bundle manifest SHA-256 为
  `f2f0fb435ac13ee7113bc240cbf3856855a1625dc11a51f264335ffddc81d7b1`。
  官方构建包含一次真实前端 build，其通过只说明构建范围。
- v7 沿用原完整 200 条有意义输入，序号 1–200、全部唯一，逐字节相同；SHA-256 为
  `3b53ca87f1e5a21acc26636d4597574baccf7024adfe6fd00a825230e3143e2f`。
  公开 Go 工作副本仍为 99 个文件；连同其他受保护原件共 333 个文件、索引和 HEAD 均不变。
  没有从宿主机编辑 Task Workspace，也没有加入答案、参考 patch 或修复提示。
- 原 v12 的全部运行文件及原 v6 的冻结程序指纹仍一致。新 profile 只调整版本／路径、
  构建与源码 CI 绑定，以及错误观察；模型、路由、32,768 应用窗口、任务、预算和分母沿用。
  40／80／120／160 轮刷新、第 100 轮空闲重启、真实压缩与后续工具／项目测试仍要求实际完成。
- 组件初始化／完整输入检查 8/8，退出 0；进程停止观察的 3 个 fixture 检查通过，退出 0。
  JavaScript 五个入口的语法检查及 Python freeze 语法检查通过。
- 文档契约 `python -B -m pytest -q tests/eval/test_ci_scope.py tests/eval/test_release_gate_workflow.py tests/eval/test_release_evidence.py`：
  23 PASS／0 SKIP／0 FAIL，退出 0。四路径秘密／机器路径／链接扫描与
  `git diff --cached --check` 均退出 0。独立 AI_REVIEWED：Standards 0／Spec 0。

此前浏览器只保留了 63 个 TypeError 名称，无法据此定位故障。本次仅让同一 driver 回调
保留脱敏后的错误消息及堆栈，仍保留全部错误，不改变页面行为。该回调的 fixture
先 RED、退出 1，再 GREEN、退出 0；检查错误详情保留与已知秘密值脱敏，原失败日志不覆盖。
这不证明原 UI 故障已修复，也不构成实际浏览器或产品验证。

## 输入与验收边界

新 profile 位于忽略目录 `deployments/docker/.local/profiles/product-long-dialogue-20261021-v7/`；
在完成实际运行前的指纹、原账本、控制服务、镜像和真实凭据检查后，才生成最终 `inputs.json`。
离线准备记录时该文件尚未生成，产品、模型和 Docker 资源均未启动或新增。
最终冻结另记录完整原生前缀、实际准入状态、运行及过程指纹，不能把此离线观察当作已满足。

构建及验证命令、退出码与原始日志保留在 `.scratch/long-dialogue-preflight-20261021/`：
`build-result.json`、`offline-preparation.json`、`browser-error-red.log` 及对应 GREEN／protocol／stop 日志。
离线观察记录先于实际封存，原账本当时的全部 40,199 个原始事件列不变；实际封存新增事实另记于上文。

源码资格绑定此前同 SHA [CI 5/5](https://github.com/huadeng408/CodeOps-Agent/actions/runs/39547005396)，
本轮构建已重新核对参与构建的实际文件指纹，没有把此前源码测试计为本轮重跑。
本轮交付为构建／组件／恢复准备；新的 200 轮、真实 Trace／Memory 与完整发布仍须各自验收。
