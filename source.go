package main

import (
	"archive/zip"
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	launcherVersion = "1.0.1"
	frpVersion      = "0.68.0"
	frpDir          = "frp_data"
	githubRepo      = "myxiaomi626-hub/frplauncher"
)

func printBanner() {
	fmt.Println()
	fmt.Println("  ╔══════════════════════════════════╗")
	fmt.Println("  ║       FRP Лаунчер v" + launcherVersion + "          ║")
	fmt.Println("  ╚══════════════════════════════════╝")
	fmt.Println()
}

func getFRPDownloadURL() string {
	goos := runtime.GOOS
	return fmt.Sprintf(
		"https://github.com/fatedier/frp/releases/download/v%s/frp_%s_%s_amd64.zip",
		frpVersion, frpVersion, goos,
	)
}

func getFRPBinaryName() string {
	if runtime.GOOS == "windows" {
		return "frpc.exe"
	}
	return "frpc"
}

func downloadWithProgress(url string, dest string) error {
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("ошибка подключения: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("сервер вернул код %d", resp.StatusCode)
	}

	total := resp.ContentLength
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()

	bar := &progressBar{total: total}
	_, err = io.Copy(io.MultiWriter(out, bar), resp.Body)
	fmt.Println()
	return err
}

type progressBar struct {
	total    int64
	written  int64
	lastDraw time.Time
}

func (pb *progressBar) Write(p []byte) (int, error) {
	n := len(p)
	pb.written += int64(n)
	if time.Since(pb.lastDraw) > 80*time.Millisecond || pb.written == pb.total {
		pb.draw()
		pb.lastDraw = time.Now()
	}
	return n, nil
}

func (pb *progressBar) draw() {
	width := 40
	var pct float64
	if pb.total > 0 {
		pct = float64(pb.written) / float64(pb.total) * 100
	}
	filled := int(float64(width) * pct / 100)
	bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
	mbDone := float64(pb.written) / 1024 / 1024
	mbTotal := float64(pb.total) / 1024 / 1024
	fmt.Printf("\r  %s %.1f / %.1f MB | %.0f%%", bar, mbDone, mbTotal, pct)
}

func extractFRP(archivePath string, destDir string) (string, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return "", err
	}

	r, err := zip.NewReader(f, info.Size())
	if err != nil {
		return "", err
	}

	binaryName := getFRPBinaryName()
	var foundPath string

	for _, file := range r.File {
		if strings.HasSuffix(file.Name, "/"+binaryName) || file.Name == binaryName {
			outPath := filepath.Join(destDir, binaryName)
			out, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
			if err != nil {
				return "", err
			}

			rc, err := file.Open()
			if err != nil {
				out.Close()
				return "", err
			}

			if _, err := io.Copy(out, rc); err != nil {
				out.Close()
				rc.Close()
				return "", err
			}
			out.Close()
			rc.Close()
			foundPath = outPath
		}
	}

	if foundPath == "" {
		return "", fmt.Errorf("бинарник frpc не найден в архиве")
	}
	return foundPath, nil
}

func downloadConfig(url string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", fmt.Errorf("не удалось получить конфиг: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d при загрузке конфига", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func parseConfig(cfg string) (serverAddr string, remotePort string) {
	for _, line := range strings.Split(cfg, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "serverAddr") || strings.HasPrefix(line, "server_addr") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				serverAddr = strings.TrimSpace(parts[1])
				serverAddr = strings.Trim(serverAddr, "\"'")
			}
		}
		if strings.HasPrefix(line, "remotePort") || strings.HasPrefix(line, "remote_port") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				remotePort = strings.TrimSpace(parts[1])
				remotePort = strings.Trim(remotePort, "\"'")
			}
		}
	}
	return
}

func getServerFlag(ip string) string {
	ip = strings.TrimSpace(ip)
	switch ip {
	case "194.31.223.177":
		return " [DE]"
	case "45.131.46.14":
		return " [RU]"
	}
	return ""
}

func checkUpdate() {
	type release struct {
		TagName string `json:"tag_name"`
	}
	client := &http.Client{Timeout: 5 * time.Second}
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", githubRepo)
	resp, err := client.Get(url)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	var rel release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return
	}
	latest := strings.TrimPrefix(rel.TagName, "v")
	if latest != "" && latest != launcherVersion {
		fmt.Printf("  [!] Доступна новая версия: v%s (у вас v%s)\n", latest, launcherVersion)
		fmt.Printf("  [!] https://github.com/%s/releases/latest\n\n", githubRepo)
	}
}


func main() {
	printBanner()
	checkUpdate()

	binaryPath := filepath.Join(frpDir, getFRPBinaryName())
	if _, err := os.Stat(binaryPath); os.IsNotExist(err) {
		fmt.Println("  [1/3] Загрузка FRP...")

		if err := os.MkdirAll(frpDir, 0755); err != nil {
			fmt.Printf("  [X] Ошибка: %v\n", err)
			os.Exit(1)
		}

		archiveURL := getFRPDownloadURL()
		archivePath := filepath.Join(frpDir, "frp.zip")

		if err := downloadWithProgress(archiveURL, archivePath); err != nil {
			fmt.Printf("  [X] Ошибка: %v\n", err)
			os.Exit(1)
		}

		fmt.Println("  [1/3] Распаковка...")
		path, err := extractFRP(archivePath, frpDir)
		if err != nil {
			fmt.Printf("  [X] Ошибка: %v\n", err)
			os.Exit(1)
		}
		binaryPath = path
		os.Remove(archivePath)
		fmt.Println("  [✓] FRP готов к работе")
	} else {
		fmt.Println("  [✓] FRP уже загружен")
	}
	fmt.Println()

	reader := bufio.NewReader(os.Stdin)
	fmt.Println("  Введите ссылку на конфиг:")
	fmt.Print("  └─> ")
	configURL, _ := reader.ReadString('\n')
	configURL = strings.TrimSpace(configURL)

	if configURL == "" {
		fmt.Println("  [X] Ссылка не введена")
		os.Exit(1)
	}

	fmt.Println("\n  [2/3] Загрузка конфига...")
	configContent, err := downloadConfig(configURL)
	if err != nil {
		fmt.Printf("  [X] %v\n", err)
		os.Exit(1)
	}

	configPath := filepath.Join(frpDir, "frpc.toml")
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		fmt.Printf("  [X] Ошибка: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("  [✓] Конфиг загружен")
	fmt.Println()

	serverAddr, remotePort := parseConfig(configContent)
	flag := getServerFlag(serverAddr)

	fmt.Println("  [3/3] Запуск FRP...")
	fmt.Println()

	cmd := exec.Command(binaryPath, "-c", configPath)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		fmt.Printf("  [X] Ошибка запуска: %v\n", err)
		os.Exit(1)
	}

	time.Sleep(1500 * time.Millisecond)

	if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
		fmt.Println("  [X] FRP завершился сразу. Проверьте конфиг.")
		os.Exit(1)
	}

	displayServer := serverAddr
	if displayServer == "" {
		displayServer = "сервер"
	}
	if remotePort == "" {
		remotePort = "????"
	}

	fmt.Println("  ╔════════════════════════════════════════╗")
	fmt.Println("  ║  ✓ FRP АКТИВЕН                         ║")
	fmt.Println("  ╠════════════════════════════════════════╣")
	fmt.Printf("  ║  Локально:  127.0.0.1:4444             ║\n")
	fmt.Printf("  ║  Удалённо:  %-26s ║\n", displayServer+":"+remotePort+flag)
	fmt.Println("  ╚════════════════════════════════════════╝")
	fmt.Println()
	fmt.Printf("  PID процесса: %d\n", cmd.Process.Pid)
	fmt.Println("  Нажмите Enter для остановки...")
	fmt.Println()

	reader.ReadString('\n')

	fmt.Println("\n  [!] Остановка FRP...")
	cmd.Process.Kill()
	cmd.Wait()
	fmt.Println("  [✓] Готово")
	fmt.Println()
}
