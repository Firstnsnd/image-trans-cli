package main

import (
	"fmt"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"
	"log"
	"os"
	"os/exec"
	"time"
)

// Config 定义了配置文件的结构
type Config struct {
	Images []string `yaml:"images"`
	Target string   `yaml:"target"`
}

// ImageResult 定义镜像处理结果
type ImageResult struct {
	SourceImage string
	TargetImage string
	Success     bool
	FailStage   string // pull, tag, or push
	Error       error
}

const (
	MaxRetries    = 3 // 最大重试次数
	RetryInterval = 3 // 重试间隔（秒）
)

func main() {
	var (
		configPath string
		verbose    bool
		dryRun     bool
	)

	rootCmd := &cobra.Command{
		Use:   "image-trans-cli",
		Short: "A CLI tool to manage Docker images",
		Long: `A CLI tool to manage Docker images that helps you:
- Pull images from source registry
- Tag images with new repository
- Push images to target registry

Example usage:
    image-trans-cli -c config.yaml

Configuration file (config.yaml) format:
  images:
    - docker.vaniot.net/nginx:latest
    - docker.vaniot.net/redis:6
  target: my-registry.com`,
		Example: `  # Process images using configuration file
    image-trans-cli -c ./config.yaml
  
  # Process images using configuration file in different path
    image-trans-cli --config /path/to/config.yaml`,
		Version: "v1.0.0",

		PreRun: func(cmd *cobra.Command, args []string) {
			fmt.Println("Starting image processing...")
		},

		PostRun: func(cmd *cobra.Command, args []string) {
			fmt.Println("Image processing completed.")
		},

		Run: func(cmd *cobra.Command, args []string) {
			if !checkDockerInstalled() {
				log.Fatal("Docker is not installed or not available in PATH. Please install Docker and try again.")
			}

			if configPath == "" {
				fmt.Println("Error: config file must be specified")
				cmd.Help()
				os.Exit(1)
			}

			config, err := loadConfig(configPath)
			if err != nil {
				log.Fatalf("Failed to load config: %v", err)
			}

			if len(config.Images) == 0 {
				log.Fatal("No images specified in the config file.")
			}

			if config.Target == "" {
				log.Fatal("Target repository is not specified in the config file.")
			}

			results := processImages(config.Images, config.Target, verbose, dryRun)
			printResults(results, verbose)
		},
	}

	// 添加命令行参数
	rootCmd.Flags().StringVarP(&configPath, "config", "c", "", "Path to the YAML configuration file")
	rootCmd.MarkFlagRequired("config")
	rootCmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose output")
	rootCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview actions without executing them")

	if err := rootCmd.Execute(); err != nil {
		log.Fatal(err)
	}
}

// checkDockerInstalled 检查系统是否安装了 Docker
func checkDockerInstalled() bool {
	cmd := exec.Command("docker", "--version")
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}

// loadConfig 从指定路径加载并解析配置文件
func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	return &config, nil
}

// processImages 处理镜像的主要逻辑
func processImages(sourceImages []string, targetRepo string, verbose bool, dryRun bool) []ImageResult {
	var results []ImageResult

	if dryRun {
		fmt.Println("DRY RUN MODE - No actual changes will be made")
	}

	for _, sourceImage := range sourceImages {
		result := ImageResult{
			SourceImage: sourceImage,
			TargetImage: fmt.Sprintf("%s/%s", targetRepo, extractImageName(sourceImage)),
			Success:     false,
		}

		if verbose {
			fmt.Printf("Processing image %s in detail:\n", sourceImage)
			fmt.Printf("  Source: %s\n", sourceImage)
			fmt.Printf("  Target: %s\n", result.TargetImage)
		} else {
			fmt.Println("Processing image:", sourceImage)
		}

		if dryRun {
			result.Success = true
			results = append(results, result)
			continue
		}

		// 拉取源镜像
		err := retryOperation(func() error {
			if verbose {
				fmt.Printf("  Pulling source image: %s\n", sourceImage)
			}
			return executeCommand("docker", "pull", sourceImage)
		}, "Pulling", verbose)

		if err != nil {
			result.FailStage = "pull"
			result.Error = err
			results = append(results, result)
			continue
		}

		// 标记镜像
		err = retryOperation(func() error {
			if verbose {
				fmt.Printf("  Tagging image as: %s\n", result.TargetImage)
			}
			return executeCommand("docker", "tag", sourceImage, result.TargetImage)
		}, "Tagging", verbose)

		if err != nil {
			result.FailStage = "tag"
			result.Error = err
			results = append(results, result)
			continue
		}

		// 推送镜像到目标仓库
		err = retryOperation(func() error {
			if verbose {
				fmt.Printf("  Pushing image to target repository: %s\n", result.TargetImage)
			}
			return executeCommand("docker", "push", result.TargetImage)
		}, "Pushing", verbose)

		if err != nil {
			result.FailStage = "push"
			result.Error = err
			results = append(results, result)
			continue
		}

		result.Success = true
		if verbose {
			fmt.Printf("  Successfully processed image: %s\n", sourceImage)
		}

		results = append(results, result)
	}

	return results
}

// retryOperation 封装重试逻辑
func retryOperation(operation func() error, operationName string, verbose bool) error {
	var lastErr error
	for attempt := 1; attempt <= MaxRetries; attempt++ {
		if err := operation(); err == nil {
			return nil
		} else {
			lastErr = err
			if attempt < MaxRetries {
				if verbose {
					fmt.Printf("  %s failed (attempt %d/%d): %v. Retrying in %d seconds...\n",
						operationName, attempt, MaxRetries, err, RetryInterval)
				}
				time.Sleep(RetryInterval * time.Second)
			}
		}
	}
	return lastErr
}

// executeCommand 执行命令行命令
func executeCommand(command string, args ...string) error {
	cmd := exec.Command(command, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// extractImageName 从完整的镜像路径中提取镜像名和标签
func extractImageName(image string) string {
	lastSlash := -1
	for i := len(image) - 1; i >= 0; i-- {
		if image[i] == '/' {
			lastSlash = i
			break
		}
	}
	if lastSlash != -1 {
		return image[lastSlash+1:]
	}
	return image
}

// printResults 打印处理结果统计
func printResults(results []ImageResult, verbose bool) {
	var successful, failed int
	fmt.Println("\nProcessing Results:")
	fmt.Println("==================")

	// 先打印成功的
	fmt.Println("\nSuccessful Transfers:")
	for _, result := range results {
		if result.Success {
			successful++
			fmt.Printf("✅ %s -> %s\n", result.SourceImage, result.TargetImage)
		}
	}

	// 再打印失败的
	fmt.Println("\nFailed Transfers:")
	for _, result := range results {
		if !result.Success {
			failed++
			fmt.Printf("❌ %s -> %s [Failed at: %s]\n",
				result.SourceImage, result.TargetImage, result.FailStage)
			if verbose && result.Error != nil {
				fmt.Printf("   Error: %v\n", result.Error)
			}
		}
	}

	// 打印总结
	fmt.Printf("\nSummary:")
	fmt.Printf("\nTotal: %d", len(results))
	fmt.Printf("\nSuccessful: %d", successful)
	fmt.Printf("\nFailed: %d\n", failed)
}
