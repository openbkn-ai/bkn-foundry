# Cypher 语法与生成器

本目录只放语法文件与生成脚本；生成产物在同级的 `../parsing/`，**已提交进仓库**，构建与运行都不需要 Java。

## 三处版本必须一致

| 项 | 值 | 位置 |
|---|---|---|
| 语法 | openCypher **M19**（即 openCypher 9） | 本目录 `Cypher.g4` |
| 生成器 | ANTLR **4.13.1** | `generate.sh` |
| 运行时 | `github.com/antlr4-go/antlr/v4` **v4.13.1** | `server/go.mod` |

生成器与运行时版本不一致会在运行期报 serialized ATN 版本错误，改任意一处都要三处同步。

## 语法来源

```
https://s3.amazonaws.com/artifacts.opencypher.org/M19/Cypher.g4
```

Apache-2.0，顶层规则 `oC_Cypher`。**不要手改** —— 需要偏离标准时在 AST 遍历阶段处理，不改语法。

## 重新生成

```sh
curl -sSLO https://repo1.maven.org/maven2/org/antlr/antlr4/4.13.1/antlr4-4.13.1-complete.jar
mv antlr4-4.13.1-complete.jar antlr-4.13.1-complete.jar
./generate.sh
```

（`www.antlr.org` 在部分网络下 SSL 握手失败，故用 Maven Central。jar 不入库。）

生成后需给 `../parsing/*.go` 补仓库许可头——ANTLR 不会生成它，与 vega-backend 的
`logic_view/sql/parsing` 做法一致。

## 为什么语法全收而子集靠遍历收窄

`Cypher.g4` 接受完整的 openCypher 9。`CREATE`、`WITH`、`UNION`、聚合函数等**在语法层全部合法**，
由 planner 在遍历 AST 时判定并拒绝。这样错误信息能说明「该构造当前不支持及其原因」，
而不是甩一个语法错误。相关用例见 `../parse_test.go`。
