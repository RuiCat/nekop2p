// Command zksetup 运行 Groth16 可信设置，生成 proving/verifying key。
package main

import (
	"bytes"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/nekop2p/nekop2p/zkcircuits/setup"
)

func main() {
	outDir := flag.String("out", "./keys/", "输出目录")
	flag.Parse()
	os.MkdirAll(*outDir, 0755)

	results, err := setup.SetupAll()
	if err != nil {
		log.Fatalf("SetupAll: %v", err)
	}

	for _, r := range results {
		fmt.Printf("[%s] 约束数: %d\n", r.Name, r.ConstraintCount)

		var buf bytes.Buffer
		r.VerifyingKey.WriteTo(&buf)
		vkPath := filepath.Join(*outDir, r.Name+".vk")
		os.WriteFile(vkPath, buf.Bytes(), 0644)
		fmt.Printf("  ✅ %s (%d bytes)\n", vkPath, buf.Len())
	}
	fmt.Printf("\n✅ 完成! %d 个电路的 .vk 已保存到 %s\n", len(results), *outDir)
}
