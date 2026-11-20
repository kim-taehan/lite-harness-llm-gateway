// llm-gateway — 폐쇄망용 LLM 게이트웨이. litellm 프록시를 대체하는 단일 실행 파일.
package main

import (
	"os"

	"llm-gateway/internal/app"
)

func main() {
	os.Exit(app.Run(os.Stdout, os.Stderr))
}
