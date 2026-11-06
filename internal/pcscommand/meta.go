package pcscommand

import (
	"fmt"
)

// RunGetMeta 执行 获取文件/目录的元信息
func RunGetMeta(targetPaths ...string) {
	targetPaths, err := matchPathByShellPattern(targetPaths...)
	if err != nil {
		fmt.Println(err)
		return
	}

	runGetMeta(GetBaiduPCS(), targetPaths...)
}
func runGetMeta(client fileReadClient, targetPaths ...string) {
	for k, targetPath := range targetPaths {
		fmt.Printf("[%d] - [%s] --------------\n", k, targetPath)
		data, err := client.FilesDirectoriesMeta(targetPath)
		if err != nil {
			fmt.Println(err)
			return
		}
		fmt.Println()
		fmt.Println(data)
	}
}
