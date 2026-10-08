package cmd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"pkg.blksails.net/bk/internal/auth"
	"pkg.blksails.net/bk/internal/docsclient"
)

type docsAPI interface {
	List(context.Context, string, docsclient.ListOptions) (*docsclient.ListResult, error)
	Content(context.Context, string, string) (*docsclient.ContentResult, error)
	Append(context.Context, string, string) error
	ListSheets(context.Context, string) (*docsclient.ListSheetsResult, error)
	ReadRange(context.Context, string, string, string) (*docsclient.ReadRangeResult, error)
	WriteRange(context.Context, string, string, string, [][]string) (*docsclient.WriteRangeResult, error)
	AddSheet(context.Context, string, string, int, int) (*docsclient.SheetInfo, error)
	DeleteSheet(context.Context, string, string) error
	ClearRange(context.Context, string, string, string) error
	DeleteDimension(context.Context, string, string, string, int, int) error
	Export(context.Context, string, string) (string, error)
	Download(context.Context, string, io.Writer) error
	Upload(context.Context, string, string) (*docsclient.UploadResult, error)
	CreateFolder(context.Context, string, string) (string, error)
	Status(context.Context) (*docsclient.AuthStatus, error)
}

const docsProviderTDocs = "tdocs"

var docsProviderFlag string

// resolveDocsProvider is the provider registry boundary. A future Feishu
// implementation can be added here without changing the command tree.
func resolveDocsProvider(raw string) (string, error) {
	provider := strings.ToLower(strings.TrimSpace(raw))
	if provider == "" {
		provider = docsProviderTDocs
	}
	switch provider {
	case docsProviderTDocs:
		return provider, nil
	default:
		return "", fmt.Errorf("不支持的在线文档 provider %q；当前支持: tdocs（腾讯文档）", raw)
	}
}

func selectedDocsProvider() (string, error) {
	return resolveDocsProvider(viper.GetString("docs.provider"))
}

func freshDocsSession(activeProfile string) (auth.Session, error) {
	base, err := newSchemaClient(schema)
	if err != nil {
		return auth.Session{}, fmt.Errorf("创建认证客户端失败: %w", err)
	}
	sess, err := auth.EnsureFresh(authConfig, activeProfile, time.Now(), authedClientSkew, auth.GoTrueRefresher{Client: base.Auth})
	if err != nil {
		return auth.Session{}, fmt.Errorf("profile %q 未登录或会话已失效，请运行 `bk auth login`: %w", activeProfile, err)
	}
	return sess, nil
}

func newDocsAPI() (docsAPI, error) {
	provider, err := selectedDocsProvider()
	if err != nil {
		return nil, err
	}
	sess, err := freshDocsSession(profile)
	if err != nil {
		return nil, err
	}
	switch provider {
	case docsProviderTDocs:
		return docsclient.New(viper.GetString("docs.endpoint"), sess.AccessToken)
	default:
		panic("unreachable docs provider: " + provider)
	}
}

var docsCmd = &cobra.Command{
	Use:   "docs",
	Short: "管理在线文档（支持 provider）",
	Long:  "使用当前 bk profile 的登录态浏览、读取、追加、上传和下载在线文档。当前支持 tdocs（腾讯文档），并为飞书等 provider 预留扩展。首次使用请运行 `bk docs auth --provider tdocs` 完成授权。",
}

var docsAuthCmd = &cobra.Command{
	Use:     "auth",
	Aliases: []string{"connect"},
	Short:   "生成腾讯文档授权链接",
	RunE: func(cmd *cobra.Command, args []string) error {
		provider, err := selectedDocsProvider()
		if err != nil {
			return err
		}
		sess, err := freshDocsSession(profile)
		if err != nil {
			return err
		}
		userID, companyID := docsIdentity(sess)
		if userID == "" {
			return fmt.Errorf("无法从当前会话解析用户 ID，请重新运行 `bk auth login`")
		}
		if provider != docsProviderTDocs {
			return fmt.Errorf("provider %q 暂不支持授权", provider)
		}
		base := strings.TrimRight(viper.GetString("docs.endpoint"), "/")
		q := url.Values{"user_id": {userID}}
		if companyID != "" {
			q.Set("company_id", companyID)
		}
		fmt.Fprintln(cmd.OutOrStdout(), base+"/oauth/login?"+q.Encode())
		fmt.Fprintln(cmd.ErrOrStderr(), "请在浏览器打开上面的链接完成腾讯文档授权，然后运行 `bk docs status` 检查状态。")
		return nil
	},
}

func docsIdentity(sess auth.Session) (string, string) {
	userID := sess.User.ID
	parts := strings.Split(sess.AccessToken, ".")
	if len(parts) != 3 {
		return userID, ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return userID, ""
	}
	var claims struct {
		Sub         string `json:"sub"`
		CompanyID   string `json:"company_id"`
		AppMetadata struct {
			CompanyID string `json:"company_id"`
		} `json:"app_metadata"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return userID, ""
	}
	if claims.Sub != "" {
		userID = claims.Sub
	}
	if claims.AppMetadata.CompanyID != "" {
		return userID, claims.AppMetadata.CompanyID
	}
	return userID, claims.CompanyID
}

var docsStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "查看腾讯文档授权状态",
	RunE: func(cmd *cobra.Command, args []string) error {
		api, err := newDocsAPI()
		if err != nil {
			return err
		}
		status, err := api.Status(cmd.Context())
		if err != nil {
			return err
		}
		if !status.Authenticated {
			fmt.Fprintln(cmd.OutOrStdout(), "腾讯文档：未授权（运行 `bk docs auth` 获取授权链接）")
			return nil
		}
		fmt.Fprintln(cmd.OutOrStdout(), "腾讯文档：已授权")
		if status.OpenID != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "OpenID: %s\n", status.OpenID)
		}
		if !status.ExpiresAt.IsZero() {
			fmt.Fprintf(cmd.OutOrStdout(), "有效期至: %s\n", status.ExpiresAt.Local().Format(time.RFC3339))
		}
		return nil
	},
}

var (
	docsListLimit                               int
	docsListCursor, docsListType, docsListQuery string
	docsListRecursive                           bool
)

var docsListCmd = &cobra.Command{
	Use: "ls [folder|路径]", Aliases: []string{"list"}, Short: "列出在线文件和文件夹",
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		api, err := newDocsAPI()
		if err != nil {
			return err
		}
		folderID := ""
		if len(args) == 1 {
			item, err := resolveDocsFile(cmd.Context(), api, args[0])
			if err != nil {
				return err
			}
			if item.Type != "folder" {
				fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\n", item.Title, item.Type, item.ID)
				return nil
			}
			folderID = item.ID
		}
		result, err := api.List(cmd.Context(), folderID, docsclient.ListOptions{Cursor: docsListCursor, Limit: docsListLimit, Type: docsListType, Query: docsListQuery, Recursive: docsListRecursive})
		if err != nil {
			return fmt.Errorf("获取文档列表失败: %w", err)
		}
		if len(result.Items) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "(空文件夹)")
			return nil
		}
		tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "路径\t类型\tID")
		for _, item := range result.Items {
			name := item.Path
			if name == "" {
				name = item.Title
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\n", name, item.Type, item.ID)
		}
		_ = tw.Flush()
		if result.HasMore && result.NextCursor != "" {
			fmt.Fprintf(cmd.ErrOrStderr(), "还有更多，使用 --cursor %s 查看下一页\n", result.NextCursor)
		}
		return nil
	},
}

var docsCatFormat, docsCatOutput string
var docsCatCmd = &cobra.Command{
	Use: "cat <id|文件名|目录/文件>", Short: "读取在线文档内容", Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		api, err := newDocsAPI()
		if err != nil {
			return err
		}
		item, err := resolveDocsFile(cmd.Context(), api, args[0])
		if err != nil {
			return err
		}
		if item.Type == "folder" {
			return fmt.Errorf("%q 是文件夹", item.Title)
		}
		result, err := api.Content(cmd.Context(), item.ID, docsCatFormat)
		if err != nil {
			return fmt.Errorf("读取文档失败: %w", err)
		}
		if docsCatOutput != "" {
			if err := os.WriteFile(docsCatOutput, []byte(result.Content), 0o644); err != nil {
				return fmt.Errorf("写入文件失败: %w", err)
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "已写入: %s\n", docsCatOutput)
			return nil
		}
		fmt.Fprint(cmd.OutOrStdout(), result.Content)
		if !strings.HasSuffix(result.Content, "\n") {
			fmt.Fprintln(cmd.OutOrStdout())
		}
		return nil
	},
}

var docsAppendInput string
var docsAppendCmd = &cobra.Command{
	Use: "append <id|文件名> [text]", Short: "在文档末尾追加文本或在表格空行追加数据", Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		var raw []byte
		var err error
		switch {
		case docsAppendInput != "":
			raw, err = os.ReadFile(docsAppendInput)
		case len(args) == 2:
			raw = []byte(args[1])
		default:
			raw, err = io.ReadAll(cmd.InOrStdin())
		}
		if err != nil {
			return err
		}
		text := strings.TrimRight(string(raw), "\r\n")
		if text == "" {
			return fmt.Errorf("没有要追加的文本")
		}
		api, err := newDocsAPI()
		if err != nil {
			return err
		}
		item, err := resolveDocsFile(cmd.Context(), api, args[0])
		if err != nil {
			return err
		}
		if err := appendDocsContent(cmd.Context(), api, item, text); err != nil {
			return fmt.Errorf("追加失败: %w", err)
		}
		fmt.Fprintln(cmd.OutOrStdout(), "已追加")
		return nil
	},
}

var docsDownloadOutput, docsDownloadFormat string
var docsDownloadCmd = &cobra.Command{
	Use: "download <id|文件名>", Short: "导出并下载在线文档", Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		api, err := newDocsAPI()
		if err != nil {
			return err
		}
		item, err := resolveDocsFile(cmd.Context(), api, args[0])
		if err != nil {
			return err
		}
		downloadURL, err := api.Export(cmd.Context(), item.ID, docsDownloadFormat)
		if err != nil {
			return fmt.Errorf("导出失败: %w", err)
		}
		out := docsDownloadOutput
		if out == "" {
			base := item.Title
			if base == "" {
				base = item.ID
			}
			out = base + "." + docsDownloadFormat
		}
		if filepath.Ext(out) == "" {
			out += "." + docsDownloadFormat
		}
		f, err := os.Create(out)
		if err != nil {
			return err
		}
		downloadErr := api.Download(cmd.Context(), downloadURL, f)
		closeErr := f.Close()
		if downloadErr != nil {
			_ = os.Remove(out)
			return downloadErr
		}
		if closeErr != nil {
			return closeErr
		}
		fmt.Fprintf(cmd.OutOrStdout(), "下载完成: %s\n", out)
		return nil
	},
}

var docsUploadFolder string
var docsUploadCmd = &cobra.Command{
	Use: "upload <file>", Short: "上传本地文件到腾讯文档", Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, err := os.Stat(args[0]); err != nil {
			return fmt.Errorf("读取上传文件失败: %w", err)
		}
		api, err := newDocsAPI()
		if err != nil {
			return err
		}
		result, err := api.Upload(cmd.Context(), args[0], docsUploadFolder)
		if err != nil {
			return fmt.Errorf("上传失败: %w", err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "上传成功，文件 ID: %s\n", result.FileID)
		if result.URL != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "访问地址: %s\n", result.URL)
		}
		return nil
	},
}

var docsMkdirParent string
var docsMkdirCmd = &cobra.Command{
	Use: "mkdir <name>", Short: "创建在线文件夹", Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		api, err := newDocsAPI()
		if err != nil {
			return err
		}
		id, err := api.CreateFolder(cmd.Context(), args[0], docsMkdirParent)
		if err != nil {
			return fmt.Errorf("创建文件夹失败: %w", err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "文件夹创建成功，ID: %s\n", id)
		return nil
	},
}

func looksLikeDocsID(s string) bool {
	if strings.Contains(s, "$") {
		return true
	}
	if strings.ContainsAny(s, " /\\\t") {
		return false
	}
	for _, r := range s {
		if r > unicode.MaxASCII {
			return false
		}
	}
	return len(s) >= 8
}

func resolveDocsFile(ctx context.Context, api docsAPI, ref string) (*docsclient.File, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, fmt.Errorf("文档名或 ID 不能为空")
	}
	if looksLikeDocsID(ref) {
		return &docsclient.File{ID: ref}, nil
	}
	parts := strings.FieldsFunc(ref, func(r rune) bool { return r == '/' || r == '\\' })
	folderID := ""
	for i, part := range parts {
		result, err := api.List(ctx, folderID, docsclient.ListOptions{Limit: 100, Query: part, Recursive: len(parts) == 1})
		if err != nil {
			return nil, fmt.Errorf("查找文档失败: %w", err)
		}
		var exact, partial []docsclient.File
		for _, item := range result.Items {
			if i < len(parts)-1 && item.Type != "folder" {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(item.Title), strings.TrimSpace(part)) {
				exact = append(exact, item)
			} else if strings.Contains(strings.ToLower(item.Title), strings.ToLower(part)) {
				partial = append(partial, item)
			}
		}
		matches := exact
		if len(matches) == 0 {
			matches = partial
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("找不到文档 %q", ref)
		}
		if len(matches) > 1 {
			return nil, fmt.Errorf("%q 匹配到多个文档，请使用完整路径或文件 ID", part)
		}
		if i == len(parts)-1 {
			return &matches[0], nil
		}
		folderID = matches[0].ID
	}
	return nil, fmt.Errorf("找不到文档 %q", ref)
}

func init() {
	viper.SetDefault("docs.provider", docsProviderTDocs)
	docsCmd.PersistentFlags().StringVar(&docsProviderFlag, "provider", "", "在线文档 provider（默认读取 docs.provider，当前支持 tdocs）")
	_ = viper.BindPFlag("docs.provider", docsCmd.PersistentFlags().Lookup("provider"))
	_ = viper.BindEnv("docs.provider", "BK_DOCS_PROVIDER")
	docsListCmd.Flags().IntVar(&docsListLimit, "limit", 20, "每页数量")
	docsListCmd.Flags().StringVar(&docsListCursor, "cursor", "", "分页游标")
	docsListCmd.Flags().StringVar(&docsListType, "type", "", "按类型过滤: doc|sheet|slide|pdf|folder")
	docsListCmd.Flags().StringVarP(&docsListQuery, "query", "q", "", "按标题过滤")
	docsListCmd.Flags().BoolVarP(&docsListRecursive, "recursive", "R", true, "递归列出子目录")
	docsCatCmd.Flags().StringVarP(&docsCatFormat, "format", "f", "md", "内容格式: md|txt|csv|json")
	docsCatCmd.Flags().StringVarP(&docsCatOutput, "output", "o", "", "写入文件")
	docsAppendCmd.Flags().StringVarP(&docsAppendInput, "input", "i", "", "从文件读取追加内容")
	docsDownloadCmd.Flags().StringVarP(&docsDownloadOutput, "output", "o", "", "输出文件")
	docsDownloadCmd.Flags().StringVarP(&docsDownloadFormat, "format", "f", "docx", "导出格式: docx|xlsx|pdf|pptx")
	docsUploadCmd.Flags().StringVar(&docsUploadFolder, "folder", "", "目标文件夹 ID")
	docsMkdirCmd.Flags().StringVar(&docsMkdirParent, "parent", "", "父文件夹 ID")
	docsCmd.AddCommand(docsAuthCmd, docsStatusCmd, docsListCmd, docsCatCmd, docsAppendCmd, docsDownloadCmd, docsUploadCmd, docsMkdirCmd, docsSheetCmd)
	rootCmd.AddCommand(docsCmd)
}
