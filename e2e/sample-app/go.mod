// 独立嵌套模块：把样例应用与 bk 主模块的包图隔离，使 `go build ./...` 不会把它
// 当作 bk 的一部分编译（嵌套 module 会被父 module 的 ./... 忽略）。
module example.com/bk-e2e-sample

go 1.25
