package main

import (
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/spf13/cobra"
	"golang.org/x/term"
	"gopkg.in/yaml.v2"
)

// version 编译时通过 -ldflags "-X main.version=v2.0.0" 注入，默认 dev
var version = "dev"

// Config 定义了配置文件的结构
type Config struct {
	Images   []string `yaml:"images"`
	Target   string   `yaml:"target"`
	Mirror   string   `yaml:"mirror"`   // Docker Hub 镜像源（可选）
	Compress bool     `yaml:"compress"` // 压缩输出 tarball，跳过 push（可选，默认 false）
	Output   string   `yaml:"output"`   // 压缩模式下输出目录（可选，默认当前目录）
}

// ImageResult 定义镜像处理结果
type ImageResult struct {
	SourceImage string
	TargetImage string
	Success     bool
	FailStage   string // pull or push
	Error       error
}

// dockerConfig 代表 Docker 兼容的 ~/.docker/config.json 文件结构
type dockerConfig struct {
	Auths map[string]dockerAuthEntry `json:"auths,omitempty"`
}

type dockerAuthEntry struct {
	Auth string `json:"auth"`
}

// timeoutKeychain 包装 authn.DefaultKeychain，带超时兜底
// 当 credential helper（如 docker-credential-osxkeychain）卡住时，回退为匿名访问
type timeoutKeychain struct {
	timeout time.Duration
}

func (tk *timeoutKeychain) Resolve(resource authn.Resource) (authn.Authenticator, error) {
	type result struct {
		auth authn.Authenticator
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		auth, err := authn.DefaultKeychain.Resolve(resource)
		ch <- result{auth, err}
	}()
	select {
	case r := <-ch:
		return r.auth, r.err
	case <-time.After(tk.timeout):
		// credential helper 卡住，fallback 为匿名访问（public registry 可用）
		return authn.Anonymous, nil
	}
}

// craneOptions 返回带超时保护的 crane 选项集合
func craneOptions() []crane.Option {
	return []crane.Option{
		crane.WithAuthFromKeychain(&timeoutKeychain{timeout: KeychainTimeout}),
		crane.WithTransport(httpTransport()),
	}
}

// httpTransport 返回带合理超时的 HTTP Transport（IPv4 优先）
func httpTransport() *http.Transport {
	return &http.Transport{
		DialContext:           dialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

// dialContext 优先尝试 IPv4，避免某些网络环境下 IPv6 超时卡住
func dialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}

	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}

	// IPv4 优先，IPv6 次之
	var ipv4, ipv6 []net.IPAddr
	for _, ip := range ips {
		if ip.IP.To4() != nil {
			ipv4 = append(ipv4, ip)
		} else {
			ipv6 = append(ipv6, ip)
		}
	}
	sorted := append(ipv4, ipv6...)

	if len(sorted) == 0 {
		return nil, fmt.Errorf("no addresses for %s", host)
	}

	// 逐个尝试，每个地址超时 DialTimeout
	var lastErr error
	for _, ip := range sorted {
		dialCtx, cancel := context.WithTimeout(ctx, DialTimeout)
		conn, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", net.JoinHostPort(ip.IP.String(), port))
		cancel()
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

const (
	MaxRetries     = 3                // 最大重试次数
	RetryInterval  = 3                // 重试间隔（秒）
	OperateTimeout = 10 * time.Minute // 单次 pull/push 操作超时
	KeychainTimeout = 5 * time.Second  // credential helper 解析超时
	DialTimeout    = 10 * time.Second  // TCP 连接超时
)

func main() {
	var (
		configPath string
		verbose    bool
		dryRun     bool
	)

	rootCmd := &cobra.Command{
		Use:   "image-trans-cli",
		Short: "A CLI tool to transfer container images between registries",
		Long: `A CLI tool to transfer container images between registries that helps you:
	- Pull images from source registry
	- Push images to target registry

No Docker daemon required — communicates directly with OCI-compatible registries.

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
		Version: version,

		PreRun: func(cmd *cobra.Command, args []string) {
			fmt.Println("Starting image processing...")
		},

		PostRun: func(cmd *cobra.Command, args []string) {
			fmt.Println("Image processing completed.")
		},

		Run: func(cmd *cobra.Command, args []string) {
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

			results := processImages(config.Images, config.Target, config.Mirror, config.Compress, config.Output, verbose, dryRun)
			printResults(results, verbose)
		},
	}

	// 添加命令行参数
	rootCmd.Flags().StringVarP(&configPath, "config", "c", "", "Path to the YAML configuration file")
	rootCmd.MarkFlagRequired("config")
	rootCmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose output")
	rootCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview actions without executing them")

	// login 子命令
	var (
		loginUsername      string
		loginPassword      string
		loginPasswordStdin bool
	)

	loginCmd := &cobra.Command{
		Use:   "login [registry]",
		Short: "Log in to a container registry and save credentials",
		Long: `Log in to a container registry and save credentials to ~/.docker/config.json.
This is compatible with Docker's credential storage — after login, both image-trans-cli
and docker CLI will share the same credentials.

If no registry is specified, defaults to Docker Hub (index.docker.io).

Examples:
  # Log in to Docker Hub
  image-trans-cli login -u myuser

  # Log in to a private registry
  image-trans-cli login registry.example.com -u admin -p mypassword

  # Log in using password from stdin (recommended for scripts)
  echo "$PASSWORD" | image-trans-cli login registry.example.com -u admin --password-stdin`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			registry := "https://index.docker.io/v1/"
			if len(args) > 0 {
				registry = normalizeRegistry(args[0])
			}
			return runLogin(registry, loginUsername, loginPassword, loginPasswordStdin)
		},
	}

	loginCmd.Flags().StringVarP(&loginUsername, "username", "u", "", "Username for registry authentication")
	loginCmd.Flags().StringVarP(&loginPassword, "password", "p", "", "Password for registry authentication (use --password-stdin for security)")
	loginCmd.Flags().BoolVar(&loginPasswordStdin, "password-stdin", false, "Read password from stdin")
	loginCmd.MarkFlagRequired("username")

	// push 子命令：将本地 tarball 推送到远程仓库
	pushCmd := &cobra.Command{
		Use:   "push <tarball> <target-image>",
		Short: "Push a local tarball to a container registry",
		Long: `Push a local tarball (.tar or .tar.gz) to a container registry.
Supports both uncompressed and gzip-compressed tarballs (auto-detected by extension).

Examples:
  # Push an uncompressed tarball
  image-trans-cli push image.tar my-registry.com/myapp:v1.0

  # Push a compressed tarball
  image-trans-cli push image.tar.gz my-registry.com/myapp:v1.0

  # Push with verbose output
  image-trans-cli push image.tar.gz my-registry.com/myapp:v1.0 -v`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			tarballPath := args[0]
			targetImage := args[1]
			return runPush(tarballPath, targetImage, verbose)
		},
	}

	pushCmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose output")

	rootCmd.AddCommand(pushCmd)
	rootCmd.AddCommand(loginCmd)

	if err := rootCmd.Execute(); err != nil {
		log.Fatal(err)
	}
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

// processImages 处理镜像的主要逻辑，使用 go-containerregistry 直接与 OCI registry 通信
func processImages(sourceImages []string, targetRepo, mirror string, compress bool, outputDir string, verbose, dryRun bool) []ImageResult {
	var results []ImageResult

	if dryRun {
		fmt.Println("DRY RUN MODE - No actual changes will be made")
	}

	for _, sourceImage := range sourceImages {
		result := processSingleImage(sourceImage, targetRepo, mirror, compress, outputDir, verbose, dryRun)
		results = append(results, result)
	}

	return results
}

// processSingleImage 处理单个镜像：从源仓库拉取，压缩模式下保存到本地文件，否则推送到目标仓库
func processSingleImage(sourceImage, targetRepo, mirror string, compress bool, outputDir string, verbose, dryRun bool) ImageResult {
	result := ImageResult{
		SourceImage: sourceImage,
		TargetImage: fmt.Sprintf("%s/%s", targetRepo, extractImageName(sourceImage)),
		Success:     false,
	}

	// 应用镜像源：仅对 Docker Hub 镜像（无 registry 前缀）适用
	pullImage := sourceImage
	if mirror != "" && !strings.Contains(sourceImage, "/") {
		pullImage = mirror + "/library/" + sourceImage
	} else if mirror != "" && !strings.Contains(strings.SplitN(sourceImage, "/", 2)[0], ".") {
		// 有 namespace 但可能仍是 Docker Hub 镜像，如 "library/nginx:latest"
		pullImage = mirror + "/" + sourceImage
	}

	if verbose {
		fmt.Printf("Processing image %s in detail:\n", sourceImage)
		fmt.Printf("  Source: %s\n", sourceImage)
		if pullImage != sourceImage {
			fmt.Printf("  Pull from mirror: %s\n", pullImage)
		}
		fmt.Printf("  Target: %s\n", result.TargetImage)
	} else {
		fmt.Println("Processing image:", sourceImage)
	}

	if dryRun {
		result.Success = true
		return result
	}

	// 确定扩展名
	ext := ".tar"
	if compress {
		ext = ".tar.gz"
	}

	// Stage 1: Pull — 从源仓库拉取镜像，保存到本地 tarball
	var tmpFile string
	err := retryOperation(func() error {
		if verbose {
			fmt.Printf("  Pulling source image: %s\n", pullImage)
		}
		var pullErr error
		tmpFile, pullErr = pullAndSaveImage(pullImage, compress, verbose)
		return pullErr
	}, "Pulling", verbose)

	if err != nil {
		result.FailStage = "pull"
		result.Error = err
		return result
	}

	// 压缩模式：保存到输出目录，不推送到远程仓库
	if compress {
		outputPath := outputPath(outputDir, result.TargetImage, ext)
		if verbose {
			fmt.Printf("  Saving to: %s\n", outputPath)
		}
		if err := os.Rename(tmpFile, outputPath); err != nil {
			// Rename 跨分区可能失败，回退为 copy+delete
			if copyErr := copyFile(tmpFile, outputPath); copyErr != nil {
				os.Remove(tmpFile)
				result.FailStage = "pull"
				result.Error = fmt.Errorf("failed to save output file: %w", copyErr)
				return result
			}
			os.Remove(tmpFile)
		}
		result.Success = true
		if verbose {
			fmt.Printf("  Successfully saved: %s\n", outputPath)
		}
		return result
	}

	// 非压缩模式：推送到远程仓库
	defer os.Remove(tmpFile)

	err = retryOperation(func() error {
		if verbose {
			fmt.Printf("  Pushing image to target repository: %s\n", result.TargetImage)
		}
		return loadAndPushImage(tmpFile, result.TargetImage, false, verbose)
	}, "Pushing", verbose)

	if err != nil {
		result.FailStage = "push"
		result.Error = err
		return result
	}

	result.Success = true
	if verbose {
		fmt.Printf("  Successfully processed image: %s\n", sourceImage)
	}

	return result
}

// outputPath 根据目标镜像名生成输出文件路径
func outputPath(outputDir, targetImage, ext string) string {
	dir := outputDir
	if dir == "" {
		dir = "."
	}
	// registry.test.com/namespace/image:tag → registry.test.com_namespace_image_tag.ext
	name := strings.NewReplacer("/", "_", ":", "_").Replace(targetImage)
	return filepath.Join(dir, name+ext)
}

// copyFile 复制文件内容
func copyFile(src, dst string) error {
	sf, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sf.Close()

	df, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer df.Close()

	_, err = io.Copy(df, sf)
	return err
}

// pullAndSaveImage 从远程仓库拉取镜像并保存到本地临时 tarball 文件
// compress 为 true 时使用 gzip 压缩，减少磁盘占用
// 返回 tarball 文件路径，调用方负责清理
func pullAndSaveImage(src string, compress, verbose bool) (string, error) {
	ref, err := name.ParseReference(src)
	if err != nil {
		return "", fmt.Errorf("failed to parse image reference %s: %w", src, err)
	}

	// 获取远程镜像描述符（manifest）
	ctx, cancel := context.WithTimeout(context.Background(), OperateTimeout)
	defer cancel()

	remoteOpts := []remote.Option{
		remote.WithAuthFromKeychain(&timeoutKeychain{timeout: KeychainTimeout}),
		remote.WithTransport(httpTransport()),
		remote.WithContext(ctx),
	}
	img, err := remote.Image(ref, remoteOpts...)
	if err != nil {
		return "", fmt.Errorf("failed to pull image %s: %w", src, err)
	}

	// 计算总大小用于进度显示
	totalSize := imageSize(img)

	// 创建临时文件
	tmpFile, err := os.CreateTemp("", "image-trans-cli-*.tar")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	// 保存镜像到 tarball（带进度）
	tag, ok := ref.(name.Tag)
	if !ok {
		tag, err = name.NewTag(src)
		if err != nil {
			tmpFile.Close()
			os.Remove(tmpPath)
			return "", fmt.Errorf("failed to create tag for %s: %w", src, err)
		}
	}

	// 写入链路：tarball → progressWriter → gzipWriter(可选) → tmpFile
	var writer io.Writer = tmpFile
	var gzipWriter *gzip.Writer
	if compress {
		gzipWriter = gzip.NewWriter(tmpFile)
		writer = gzipWriter
	}

	if verbose && totalSize > 0 {
		desc := ""
		if compress {
			desc = " (gzip)"
		}
		fmt.Printf("  Downloading %s%s...\n", humanSize(totalSize), desc)
		writer = &progressWriter{w: writer, total: totalSize, label: "  "}
	}

	err = tarball.Write(tag, img, writer)
	if gzipWriter != nil {
		gzipWriter.Close()
	}
	tmpFile.Close()

	if err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("failed to save image to tarball: %w", err)
	}

	if verbose {
		fmt.Println() // 换行，结束进度行
	}

	return tmpPath, nil
}

// imageSize 返回镜像所有层 + config 的总大小
func imageSize(img v1.Image) int64 {
	manifest, err := img.Manifest()
	if err != nil {
		return 0
	}
	total := manifest.Config.Size
	for _, layer := range manifest.Layers {
		total += layer.Size
	}
	return total
}

// progressWriter 在写入时显示下载进度
type progressWriter struct {
	w        io.Writer
	total    int64
	written  int64
	label    string
	lastLog  time.Time
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	n, err := pw.w.Write(p)
	pw.written += int64(n)
	if time.Since(pw.lastLog) > 500*time.Millisecond {
		fmt.Printf("\r%s%s / %s", pw.label, humanSize(pw.written), humanSize(pw.total))
		pw.lastLog = time.Now()
	}
	return n, err
}

// humanSize 将字节数转换为人类可读格式
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// loadAndPushImage 从本地 tarball 加载镜像并推送到目标仓库
// compress 为 true 时使用 gzip 解压读取
func loadAndPushImage(tarballPath, dst string, compress, verbose bool) error {
	var img v1.Image
	var err error

	if compress {
		img, err = loadCompressedImage(tarballPath)
	} else {
		img, err = crane.Load(tarballPath)
	}
	if err != nil {
		return fmt.Errorf("failed to load image from tarball: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), OperateTimeout)
	defer cancel()

	opts := append(craneOptions(), crane.WithContext(ctx))
	if err := crane.Push(img, dst, opts...); err != nil {
		return fmt.Errorf("failed to push image %s: %w", dst, err)
	}

	return nil
}

// loadCompressedImage 从 gzip 压缩的 tarball 加载镜像
func loadCompressedImage(path string) (v1.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("failed to decompress: %w", err)
	}
	defer gzr.Close()

	// 解压到临时文件
	tmp, err := os.CreateTemp("", "image-trans-cli-decompressed-*.tar")
	if err != nil {
		return nil, err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := io.Copy(tmp, gzr); err != nil {
		tmp.Close()
		return nil, fmt.Errorf("failed to decompress tarball: %w", err)
	}
	tmp.Close()

	return tarball.ImageFromPath(tmpPath, nil)
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

// --- login 子命令相关函数 ---

// normalizeRegistry 规范化 registry 地址
func normalizeRegistry(registry string) string {
	registry = strings.TrimPrefix(registry, "https://")
	registry = strings.TrimPrefix(registry, "http://")
	return registry
}

// getDockerConfigPath 返回 Docker 兼容的认证配置文件路径
// 优先使用 DOCKER_CONFIG 环境变量，否则使用 ~/.docker/config.json
func getDockerConfigPath() (string, error) {
	if dir := os.Getenv("DOCKER_CONFIG"); dir != "" {
		return filepath.Join(dir, "config.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot find home directory: %w", err)
	}
	return filepath.Join(home, ".docker", "config.json"), nil
}

// loadDockerConfig 加载 Docker 认证配置文件，不存在则返回空配置
func loadDockerConfig(path string) (*dockerConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &dockerConfig{Auths: make(map[string]dockerAuthEntry)}, nil
		}
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg dockerConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}
	if cfg.Auths == nil {
		cfg.Auths = make(map[string]dockerAuthEntry)
	}
	return &cfg, nil
}

// saveDockerConfig 保存 Docker 认证配置文件，确保目录存在且权限正确
func saveDockerConfig(path string, cfg *dockerConfig) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	data, err := json.MarshalIndent(cfg, "", "    ")
	if err != nil {
		return fmt.Errorf("failed to encode config: %w", err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}
	return nil
}

// readPassword 交互式读取密码（不回显）
func readPassword() (string, error) {
	fmt.Print("Password: ")
	pass, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return "", fmt.Errorf("failed to read password: %w", err)
	}
	return string(pass), nil
}

// validateCredentials 通过向 registry 发起请求来验证凭证是否有效
func validateCredentials(registry, username, password string) error {
	reg, err := name.NewRegistry(registry)
	if err != nil {
		return fmt.Errorf("invalid registry %q: %w", registry, err)
	}

	auth := authn.FromConfig(authn.AuthConfig{
		Username: username,
		Password: password,
	})

	_, err = transport.NewWithContext(context.Background(), reg, auth, http.DefaultTransport, nil)
	if err != nil {
		return fmt.Errorf("login failed: %w", err)
	}

	return nil
}

// runLogin 执行 login 命令：验证凭证并写入 ~/.docker/config.json
func runLogin(registry, username, password string, passwordStdin bool) error {
	// 获取密码
	if passwordStdin {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("failed to read password from stdin: %w", err)
		}
		password = strings.TrimRight(string(data), "\r\n")
	} else if password == "" {
		var err error
		password, err = readPassword()
		if err != nil {
			return err
		}
	}

	if strings.TrimSpace(username) == "" {
		return fmt.Errorf("username cannot be empty")
	}
	if password == "" {
		return fmt.Errorf("password cannot be empty")
	}

	// 验证凭证
	fmt.Printf("Logging in to %s as %s...\n", registry, username)
	if err := validateCredentials(registry, username, password); err != nil {
		return err
	}

	// 写入配置
	configPath, err := getDockerConfigPath()
	if err != nil {
		return err
	}

	cfg, err := loadDockerConfig(configPath)
	if err != nil {
		return err
	}

	auth := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	cfg.Auths[registry] = dockerAuthEntry{Auth: auth}

	if err := saveDockerConfig(configPath, cfg); err != nil {
		return err
	}

	fmt.Printf("Login succeeded. Credentials saved to %s\n", configPath)
	return nil
}

// --- push 子命令相关函数 ---

// isCompressed 根据文件扩展名判断是否为 gzip 压缩
func isCompressed(path string) bool {
	return strings.HasSuffix(path, ".gz") || strings.HasSuffix(path, ".tgz")
}

// runPush 将本地 tarball 推送到远程仓库
func runPush(tarballPath, targetImage string, verbose bool) error {
	if _, err := os.Stat(tarballPath); err != nil {
		return fmt.Errorf("tarball not found: %s", tarballPath)
	}

	compress := isCompressed(tarballPath)
	if verbose {
		mode := "uncompressed"
		if compress {
			mode = "gzip-compressed"
		}
		fmt.Printf("Loading %s tarball: %s\n", mode, tarballPath)
	}

	// 加载 tarball（自动解压）
	var img v1.Image
	var err error
	if compress {
		img, err = loadCompressedImage(tarballPath)
	} else {
		img, err = crane.Load(tarballPath)
	}
	if err != nil {
		return fmt.Errorf("failed to load tarball: %w", err)
	}

	if verbose {
		fmt.Printf("Pushing to: %s\n", targetImage)
	}

	ctx, cancel := context.WithTimeout(context.Background(), OperateTimeout)
	defer cancel()

	opts := append(craneOptions(), crane.WithContext(ctx))
	if err := crane.Push(img, targetImage, opts...); err != nil {
		return fmt.Errorf("failed to push image %s: %w", targetImage, err)
	}

	fmt.Printf("Successfully pushed %s -> %s\n", tarballPath, targetImage)
	return nil
}
