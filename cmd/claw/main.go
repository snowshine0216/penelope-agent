// cmd/claw/main.go
package main

import (
	"context"
	"log"
	"os"

	"github.com/snowshine0216/penelope-agent/internal/engine"
	"github.com/snowshine0216/penelope-agent/internal/provider"
	"github.com/snowshine0216/penelope-agent/internal/tools"
)

func main() {
	workDir, _ := os.Getwd()

	// 1. 初始化真实的 Provider大脑。
	// 留空模型名时，会从 .env / 环境变量里的 LLM_API_KEY、LLM_BASE_URL、LLM_MODEL 读取配置。
	llmProvider, err := provider.NewZhipuOpenAIProvider("")
	if err != nil {
		log.Fatalf("init provider: %v", err)
	}

	// 2. 注入伪造的工具注册表
	registry := tools.NewRegistry()

	// 4. 将真实的 ReadFile 工具挂载到注册表中
	registry.Register(tools.NewReadFileTool(workDir))
	registry.Register(tools.NewWriteFileTool(workDir))
	registry.Register(tools.NewBashTool(workDir))

	// 5. 实例化核心引擎，由于任务简单，我们关闭思考阶段 (EnableThinking = false) 以加快速度
	eng := engine.NewAgentEngine(llmProvider, registry, workDir, false)

	// 6. 下发一个必须通过真实工具才能完成的任务
	// 发起一个需要连贯物理动作的任务 prompt := ` 请帮我执行以下操作： 1. 用 bash 查看一下我当前电脑的 Go 版本。 2. 帮我写一个简单的 helloworld.go 文件，输出 "Hello, penelope-agent!"。 3. 用 bash 编译并运行这个 go 文件，确认它能正常工作。 `
	prompt := ` 请帮我执行以下操作： 1. 用 bash 查看一下我当前电脑的 Go 版本。 2. 帮我写一个简单的 helloworld.go 文件，输出 "Hello, penelope-agent!"。 3. 用 bash 编译并运行这个 go 文件，确认它能正常工作。 `

	err = eng.Run(context.Background(), prompt)
	if err != nil {
		log.Fatalf("引擎运行崩溃: %v", err)
	}
}
