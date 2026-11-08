package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"pkg.blksails.net/bk/internal/metarails"
)

const defaultMaterialsEndpoint = "https://blksails.cn"

type metarailsAPI interface {
	Download(ctx context.Context, entries []metarails.DownloadEntry, w io.Writer) error
	Upload(ctx context.Context, paths []string, materialType string, folderID int64, tags []string) (*metarails.UploadResult, error)
}

var newMetarailsAPI = func() (metarailsAPI, error) {
	sess, err := freshDocsSession(profile)
	if err != nil {
		return nil, err
	}
	return metarails.New(viper.GetString("materials.endpoint"), sess.AccessToken)
}

var metarailsCmd = &cobra.Command{
	Use:     "materials",
	Aliases: []string{"metarails"},
	Short:   "通过 Webapp 批量下载和上传素材",
	RunE:    func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
}

var (
	metarailsDownloadOutput string
	metarailsUploadType     string
	metarailsUploadFolder   int64
	metarailsUploadTags     []string
)

var metarailsDownloadCmd = &cobra.Command{
	Use:   "download <素材ID[=ZIP内路径]>...",
	Short: "调用 Webapp 素材批量接口下载 ZIP",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		entries, err := parseDownloadEntries(args)
		if err != nil {
			return err
		}
		api, err := newMetarailsAPI()
		if err != nil {
			return err
		}
		output := metarailsDownloadOutput
		if output == "" {
			output = "素材下载.zip"
		}
		output, err = filepath.Abs(output)
		if err != nil {
			return fmt.Errorf("解析输出路径失败: %w", err)
		}
		if err := os.MkdirAll(filepath.Dir(output), 0755); err != nil {
			return fmt.Errorf("创建输出目录失败: %w", err)
		}
		tmp, err := os.CreateTemp(filepath.Dir(output), ".metarails-download-*.zip")
		if err != nil {
			return fmt.Errorf("创建临时文件失败: %w", err)
		}
		tmpName := tmp.Name()
		committed := false
		defer func() {
			if !committed {
				_ = os.Remove(tmpName)
			}
		}()
		if err := api.Download(cmd.Context(), entries, tmp); err != nil {
			_ = tmp.Close()
			return err
		}
		if err := tmp.Sync(); err != nil {
			_ = tmp.Close()
			return err
		}
		if err := tmp.Close(); err != nil {
			return err
		}
		if err := os.Rename(tmpName, output); err != nil {
			return fmt.Errorf("保存下载文件失败: %w", err)
		}
		committed = true
		fmt.Fprintf(cmd.OutOrStdout(), "已下载 %d 个素材到 %s\n", len(entries), output)
		return nil
	},
}

var metarailsUploadCmd = &cobra.Command{
	Use:   "upload <文件>...",
	Short: "调用 Webapp 素材上传接口批量上传",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		paths, materialType, err := validateUploads(args, metarailsUploadType)
		if err != nil {
			return err
		}
		api, err := newMetarailsAPI()
		if err != nil {
			return err
		}
		result, err := api.Upload(cmd.Context(), paths, materialType, metarailsUploadFolder, metarailsUploadTags)
		if err != nil {
			return err
		}
		for _, item := range result.Results {
			if item.Error != "" {
				fmt.Fprintf(cmd.ErrOrStderr(), "失败 %s: %s\n", item.FileName, item.Error)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "%d\t%s\t%s\n", item.MaterialID, item.FileName, item.FileURL)
			}
		}
		fmt.Fprintf(cmd.OutOrStdout(), "上传完成：成功 %d，失败 %d，共 %d\n", result.Success, result.Fail, result.Total)
		if result.Fail > 0 {
			return fmt.Errorf("%d 个素材上传失败", result.Fail)
		}
		return nil
	},
}

func parseDownloadEntries(args []string) ([]metarails.DownloadEntry, error) {
	entries := make([]metarails.DownloadEntry, 0, len(args))
	for _, raw := range args {
		idText, path, hasPath := strings.Cut(raw, "=")
		id, err := strconv.ParseInt(idText, 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("无效素材 ID %q", idText)
		}
		if !hasPath {
			path = idText
		}
		path = filepath.ToSlash(strings.TrimSpace(path))
		clean := filepath.ToSlash(filepath.Clean(path))
		if path == "" || filepath.IsAbs(path) || clean == ".." || strings.HasPrefix(clean, "../") {
			return nil, fmt.Errorf("无效 ZIP 内路径 %q", path)
		}
		entries = append(entries, metarails.DownloadEntry{ID: id, Path: clean})
	}
	return entries, nil
}

func validateUploads(args []string, requestedType string) ([]string, string, error) {
	paths := make([]string, 0, len(args))
	resolvedType := strings.ToUpper(strings.TrimSpace(requestedType))
	for _, raw := range args {
		path, err := filepath.Abs(raw)
		if err != nil {
			return nil, "", err
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, "", fmt.Errorf("读取 %q 失败: %w", raw, err)
		}
		if !info.Mode().IsRegular() {
			return nil, "", fmt.Errorf("%q 不是普通文件", raw)
		}
		inferred := inferMaterialType(path)
		if resolvedType == "" || resolvedType == "AUTO" {
			resolvedType = inferred
		}
		if inferred != resolvedType && (requestedType == "" || strings.EqualFold(requestedType, "auto")) {
			return nil, "", fmt.Errorf("批量上传只支持同一种素材类型：%s 推断为 %s，已有类型为 %s；请分批上传或指定 --type", raw, inferred, resolvedType)
		}
		paths = append(paths, path)
	}
	switch resolvedType {
	case "IMAGE", "VIDEO", "AUDIO", "SCRIPT":
	default:
		return nil, "", fmt.Errorf("无效素材类型 %q，需为 IMAGE、VIDEO、AUDIO 或 SCRIPT", requestedType)
	}
	return paths, resolvedType, nil
}

func inferMaterialType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".svg", ".avif":
		return "IMAGE"
	case ".mp4", ".mov", ".avi", ".mkv", ".webm", ".m4v":
		return "VIDEO"
	case ".mp3", ".wav", ".aac", ".m4a", ".flac", ".ogg":
		return "AUDIO"
	default:
		return "SCRIPT"
	}
}

func init() {
	viper.SetDefault("materials.endpoint", defaultMaterialsEndpoint)
	_ = viper.BindEnv("materials.endpoint", "BK_MATERIALS_ENDPOINT", "BK_METARAILS_ENDPOINT", "BK_WEBAPP_ENDPOINT")
	metarailsCmd.PersistentFlags().String("endpoint", defaultMaterialsEndpoint, "Webapp API 地址")
	_ = viper.BindPFlag("materials.endpoint", metarailsCmd.PersistentFlags().Lookup("endpoint"))
	metarailsDownloadCmd.Flags().StringVarP(&metarailsDownloadOutput, "output", "o", "", "ZIP 输出路径（默认 素材下载.zip）")
	metarailsUploadCmd.Flags().StringVarP(&metarailsUploadType, "type", "t", "auto", "素材类型：auto|IMAGE|VIDEO|AUDIO|SCRIPT")
	metarailsUploadCmd.Flags().Int64Var(&metarailsUploadFolder, "folder", 0, "目标素材目录 ID")
	metarailsUploadCmd.Flags().StringSliceVar(&metarailsUploadTags, "tag", nil, "素材标签（可重复或逗号分隔）")
	metarailsCmd.AddCommand(metarailsDownloadCmd, metarailsUploadCmd)
	rootCmd.AddCommand(metarailsCmd)
}
