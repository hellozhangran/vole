// Vole：macOS 磁盘分析（清理等功能后续加）。
package main

import (
	"log"

	"vole/internal/app"
)

func main() {
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
