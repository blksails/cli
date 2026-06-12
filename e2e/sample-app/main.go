// 命令 bk-e2e-sample 是全生命周期 e2e 测试用的最小可部署样例应用：
// 一个监听 $PORT 的 HTTP 服务，根路径回一句问候，/healthz 回 200。
// 真实主机模式（见 ../README.md）可把它 git push 到 Dokku 后用 BK_E2E_APP_URL 探测。
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
)

func main() {
	name := os.Getenv("APP_NAME")
	if name == "" {
		name = "bk-e2e-sample"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "hello from %s\n", name)
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "5000"
	}
	addr := ":" + port
	log.Printf("%s listening on %s", name, addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
