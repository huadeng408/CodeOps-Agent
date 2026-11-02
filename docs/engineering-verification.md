# 工程验证入口

使用公共命令运行明确的工程检查，生成可追溯的 Verification Receipt。
入口目前要求Windows Job约束进程树；其他平台在派发命令或创建产物前拒绝。
本入口的结果说明所列工程检查；真实 Agent 任务、模型用量、故障恢复与发布验收继续使用各自入口。

## 定向检查

在仓库根目录准备检查计划，例如将下列内容保存到已忽略的运行目录：

```json
{
  "schema_version": 1,
  "scope_kind": "targeted",
  "scope_name": "Session 定向回归",
  "checks": [
    {
      "id": "session",
      "argv": ["go", "test", "-vet=off", "-p", "1", "-json", "./internal/session", "-run", "TestSessionRunnerRecoversEvaluationPendingApprovalWithoutOperator", "-count=1"],
      "cwd": ".",
      "timeout_seconds": 600
    }
  ]
}
```

运行：

```powershell
python -m scripts.verify --plan .runtime/verification/session-plan.json --output .runtime/verification --run-id session-001
```

`argv`逐项传递给真实程序，使用显式工作目录和超时。输出根位于仓库外，或使用仓库已有的Git忽略目录，以便源码变化检查正确区分产物。
运行标识每次唯一；入口保护已有产物。

结果包含简短JSON摘要、`summary.json`、计划、原始脱敏日志和`checksums.sha256`。
Go JSON记录终态测试事件（包括子测试）及包分母；pytest记录报告的通过、失败、跳过等分母。
失败的Go运行保留已观察到的事件计数；JSON不能证明尚未启动的测试范围，因此完整分母记为unknown。
无法确认完整测试分母时显式记录unknown，而非零。

## 最终完整检查

完整计划只指定固定范围：

```json
{"schema_version": 1, "scope_kind": "full"}
```

```powershell
python -m scripts.verify --plan .runtime/verification/full-plan.json --output .runtime/verification --run-id full-001
```

该模式依项目契约串行执行新鲜Go／Python回归、vet、相关构建、编译／导入和检查；实际前端改动时增加前端测试和构建。
Go Agent构建产物在本次运行的`bin`目录，供后续核对源码与checksum后使用。
完整模式固定测试命令，清除会缩小范围的默认选择参数；显式开启真实集成的运行环境由预检拒绝，保持工程检查和真实运行验收分开。
入口按`go.mod`选择已安装或已缓存的实际Go SDK，并保留配置的依赖／构建缓存；子进程使用同一SDK，禁止自动下载，收据记录实际版本和二进制checksum。
Python编译检查使用Git已记录及未忽略的新增项目源码清单，强制重新编译当前源码；下载的评测仓库和其他忽略物料保留各自范围。清单及其checksum随本次产物保存，diff检查同时覆盖暂存和未暂存的改动。

受管理的同仓库重负载流程共用一个执行槽。排队超时可通过`--queue-timeout-seconds`设置。
超时或中断时记录已执行尝试及所拥有进程的停止状态；停止未确认时结束后续派发。
退出码0表示声明范围通过，1表示明确失败，3表示范围受阻；参数／计划拒绝返回2，中断返回130。

## 明确允许的定向结果复用

首轮仅开放封闭的只读格式检查：

```json
{
  "schema_version": 1,
  "scope_kind": "targeted",
  "scope_name": "指定Go文件格式检查",
  "checks": [
    {
      "id": "format",
      "argv": ["gofmt", "-l", "internal/example.go"],
      "cwd": ".",
      "timeout_seconds": 30,
      "reuse_policy": "readonly_gofmt_files_v1"
    }
  ]
}
```

把文件名替换为实际的明确文件清单。该策略要求只读`gofmt -l`，输出为空才通过；Go源码行尾也是格式输入的一部分。
入口核对实际文件、权限、工具及运行依赖、有效环境、计划和源码身份；超出已审计范围或无法确认时重新执行。

首次运行后，可以显式引用同一输出根中原始通过记录：

```powershell
python -m scripts.verify --plan .runtime/verification/format-plan.json --output .runtime/verification --run-id format-002 --reuse-from .runtime/verification/format-001/summary.json
```

命中时，新记录标为`reused`，本次执行数为0，保留原收据、时段、分母、日志与checksum引用。
输入变化、原记录损坏、失败、不完整或unknown时重新执行，并保留原因。
入口记录原收据校验、输入捕获和匹配的核验成本；达到原命令执行耗时的80%时直接重跑。
门槛对每项候选保守计入完整的原收据校验成本，不能将多项候选的核验秒数相加为总经过时间。
普通Go／pytest、外部服务、故障恢复和性能测量每次执行；完整模式拒绝复用参数。

源记录和当前记录分别保留，可按来源追溯。工程收据不承担Session状态或产品准入职责。

## 结果与限制

已确认的实现、测量、失败分母和当前外部阻塞见[本阶段证据](evidence/engineering-execution-efficiency-2026-11-02.md)。
判断效率时同时检查完整质量分母和端到端经过时间；输入核对本身也有成本，构建次数或token减少不自动等于墙钟加速。
