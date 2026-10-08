package cmd

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"pkg.blksails.net/bk/internal/docsclient"
)

const sheetRequestCellLimit = 10_000

var (
	docsSheetWriteInput string
	docsSheetAddRows    int
	docsSheetAddCols    int
)

var docsSheetCmd = &cobra.Command{
	Use:   "sheet",
	Short: "读取和修改在线表格",
}

var docsSheetListCmd = &cobra.Command{
	Use:   "ls <id|文件名|目录/文件>",
	Short: "列出子表",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		api, item, err := docsAPIAndFile(cmd, args[0])
		if err != nil {
			return err
		}
		result, err := api.ListSheets(cmd.Context(), item.ID)
		if err != nil {
			return fmt.Errorf("获取子表失败: %w", err)
		}
		if len(result.Sheets) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "(没有子表)")
			return nil
		}
		tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "SHEET_ID\t标题\t行数\t列数")
		for _, sheet := range result.Sheets {
			fmt.Fprintf(tw, "%s\t%s\t%d\t%d\n", sheet.SheetID, sheet.Title, sheet.RowCount, sheet.ColumnCount)
		}
		return tw.Flush()
	},
}

var docsSheetReadCmd = &cobra.Command{
	Use:   "read <id|文件名|目录/文件> <sheet_id|子表名> <range>",
	Short: "读取单元格区域（TSV 输出）",
	Args:  cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		api, item, err := docsAPIAndFile(cmd, args[0])
		if err != nil {
			return err
		}
		sheet, err := resolveDocsSheet(cmd.Context(), api, item.ID, args[1])
		if err != nil {
			return err
		}
		result, err := api.ReadRange(cmd.Context(), item.ID, sheet.SheetID, args[2])
		if err != nil {
			return fmt.Errorf("读取区域失败: %w", err)
		}
		writer := csv.NewWriter(cmd.OutOrStdout())
		writer.Comma = '\t'
		for _, row := range result.Values {
			if err := writer.Write(row); err != nil {
				return err
			}
		}
		writer.Flush()
		return writer.Error()
	},
}

var docsSheetWriteCmd = &cobra.Command{
	Use:   "write <id|文件名|目录/文件> <sheet_id|子表名> <range>",
	Short: "写入单元格区域（CSV 或 TSV 输入）",
	Args:  cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		input := cmd.InOrStdin()
		if docsSheetWriteInput != "" {
			file, err := os.Open(docsSheetWriteInput)
			if err != nil {
				return fmt.Errorf("打开输入文件失败: %w", err)
			}
			defer file.Close()
			input = file
		}
		values, err := readDelimitedRows(input)
		if err != nil {
			return err
		}
		api, item, err := docsAPIAndFile(cmd, args[0])
		if err != nil {
			return err
		}
		sheet, err := resolveDocsSheet(cmd.Context(), api, item.ID, args[1])
		if err != nil {
			return err
		}
		result, err := api.WriteRange(cmd.Context(), item.ID, sheet.SheetID, args[2], values)
		if err != nil {
			return fmt.Errorf("写入区域失败: %w", err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "已写入 %d 行 / %d 单元格\n", result.UpdatedRows, result.UpdatedCells)
		return nil
	},
}

var docsSheetAddCmd = &cobra.Command{
	Use:   "add <id|文件名|目录/文件> <title>",
	Short: "添加子表",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if docsSheetAddRows < 1 || docsSheetAddCols < 1 {
			return fmt.Errorf("rows 和 cols 必须是大于 0 的整数")
		}
		api, item, err := docsAPIAndFile(cmd, args[0])
		if err != nil {
			return err
		}
		sheet, err := api.AddSheet(cmd.Context(), item.ID, args[1], docsSheetAddRows, docsSheetAddCols)
		if err != nil {
			return fmt.Errorf("添加子表失败: %w", err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "已添加 %s (%s)\n", sheet.Title, sheet.SheetID)
		return nil
	},
}

var docsSheetRemoveCmd = &cobra.Command{
	Use:     "rm <id|文件名|目录/文件> <sheet_id|子表名>",
	Aliases: []string{"remove"},
	Short:   "删除子表",
	Args:    cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		api, item, err := docsAPIAndFile(cmd, args[0])
		if err != nil {
			return err
		}
		sheet, err := resolveDocsSheet(cmd.Context(), api, item.ID, args[1])
		if err != nil {
			return err
		}
		if err := api.DeleteSheet(cmd.Context(), item.ID, sheet.SheetID); err != nil {
			return fmt.Errorf("删除子表失败: %w", err)
		}
		fmt.Fprintln(cmd.OutOrStdout(), "已删除子表")
		return nil
	},
}

var docsSheetClearCmd = &cobra.Command{
	Use:   "clear <id|文件名|目录/文件> <sheet_id|子表名> <range>",
	Short: "清空单元格区域",
	Args:  cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		api, item, err := docsAPIAndFile(cmd, args[0])
		if err != nil {
			return err
		}
		sheet, err := resolveDocsSheet(cmd.Context(), api, item.ID, args[1])
		if err != nil {
			return err
		}
		if err := api.ClearRange(cmd.Context(), item.ID, sheet.SheetID, args[2]); err != nil {
			return fmt.Errorf("清空区域失败: %w", err)
		}
		fmt.Fprintln(cmd.OutOrStdout(), "已清空")
		return nil
	},
}

var docsSheetDeleteRowsCmd = &cobra.Command{
	Use:   "delete-rows <id|文件名|目录/文件> <sheet_id|子表名> <start> <end>",
	Short: "删除连续行（从 1 开始，包含首尾）",
	Args:  cobra.ExactArgs(4),
	RunE:  docsSheetDeleteDimension("ROWS"),
}

var docsSheetDeleteColsCmd = &cobra.Command{
	Use:   "delete-cols <id|文件名|目录/文件> <sheet_id|子表名> <start> <end>",
	Short: "删除连续列（从 1 开始，包含首尾）",
	Args:  cobra.ExactArgs(4),
	RunE:  docsSheetDeleteDimension("COLUMNS"),
}

func docsSheetDeleteDimension(dimension string) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		start, err := strconv.Atoi(args[2])
		if err != nil || start < 1 {
			return fmt.Errorf("start 必须是大于 0 的整数")
		}
		end, err := strconv.Atoi(args[3])
		if err != nil || end < start {
			return fmt.Errorf("end 必须是大于等于 start 的整数")
		}
		api, item, err := docsAPIAndFile(cmd, args[0])
		if err != nil {
			return err
		}
		sheet, err := resolveDocsSheet(cmd.Context(), api, item.ID, args[1])
		if err != nil {
			return err
		}
		if err := api.DeleteDimension(cmd.Context(), item.ID, sheet.SheetID, dimension, start, end); err != nil {
			return fmt.Errorf("删除失败: %w", err)
		}
		fmt.Fprintln(cmd.OutOrStdout(), "已删除")
		return nil
	}
}

func docsAPIAndFile(cmd *cobra.Command, ref string) (docsAPI, *docsclient.File, error) {
	api, err := newDocsAPI()
	if err != nil {
		return nil, nil, err
	}
	item, err := resolveDocsFile(cmd.Context(), api, ref)
	if err != nil {
		return nil, nil, err
	}
	if item.Type != "" && item.Type != "sheet" {
		return nil, nil, fmt.Errorf("%q 不是在线表格", item.Title)
	}
	return api, item, nil
}

func resolveDocsSheet(ctx context.Context, api docsAPI, fileID, ref string) (*docsclient.SheetInfo, error) {
	result, err := api.ListSheets(ctx, fileID)
	if err != nil {
		return nil, fmt.Errorf("获取子表失败: %w", err)
	}
	for i := range result.Sheets {
		sheet := &result.Sheets[i]
		if sheet.SheetID == ref || strings.EqualFold(strings.TrimSpace(sheet.Title), strings.TrimSpace(ref)) {
			return sheet, nil
		}
	}
	return nil, fmt.Errorf("找不到子表 %q", ref)
}

func readDelimitedRows(input io.Reader) ([][]string, error) {
	raw, err := io.ReadAll(input)
	if err != nil {
		return nil, err
	}
	text := strings.TrimRight(string(raw), "\r\n")
	if text == "" {
		return nil, fmt.Errorf("没有读到任何数据")
	}
	reader := csv.NewReader(strings.NewReader(text))
	if strings.Contains(strings.SplitN(text, "\n", 2)[0], "\t") {
		reader.Comma = '\t'
	}
	reader.FieldsPerRecord = -1
	rows, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("解析 CSV/TSV 失败: %w", err)
	}
	return normalizeRows(rows)
}

func normalizeRows(rows [][]string) ([][]string, error) {
	maxCols := 0
	for _, row := range rows {
		if len(row) > maxCols {
			maxCols = len(row)
		}
	}
	if len(rows) == 0 || maxCols == 0 {
		return nil, fmt.Errorf("没有读到任何数据")
	}
	if len(rows) > 1000 || len(rows)*maxCols > sheetRequestCellLimit {
		return nil, fmt.Errorf("单次最多写入 1000 行 / 10000 个单元格")
	}
	for i := range rows {
		if len(rows[i]) < maxCols {
			rows[i] = append(rows[i], make([]string, maxCols-len(rows[i]))...)
		}
	}
	return rows, nil
}

func appendDocsContent(ctx context.Context, api docsAPI, item *docsclient.File, text string) error {
	if item.Type != "" && item.Type != "sheet" {
		return api.Append(ctx, item.ID, text)
	}
	sheets, err := api.ListSheets(ctx, item.ID)
	if err != nil {
		if item.Type == "sheet" {
			return err
		}
		// A raw file ID carries no type. If it is not a sheet, preserve the
		// original document append behavior.
		return api.Append(ctx, item.ID, text)
	}
	if len(sheets.Sheets) == 0 {
		return fmt.Errorf("在线表格没有子表")
	}
	rows, err := readDelimitedRows(strings.NewReader(text))
	if err != nil {
		return err
	}
	return appendSheetRows(ctx, api, item.ID, sheets.Sheets[0], rows)
}

func appendSheetRows(ctx context.Context, api docsAPI, fileID string, sheet docsclient.SheetInfo, rows [][]string) error {
	rows, err := normalizeRows(rows)
	if err != nil {
		return err
	}
	cols := len(rows[0])
	if sheet.RowCount <= 0 || sheet.ColumnCount <= 0 {
		return fmt.Errorf("子表尺寸无效")
	}
	if cols > sheet.ColumnCount {
		return fmt.Errorf("追加数据有 %d 列，子表只有 %d 列", cols, sheet.ColumnCount)
	}
	if sheet.ColumnCount > sheetRequestCellLimit {
		return fmt.Errorf("子表列数超过单次读取上限")
	}

	empty := make([]bool, sheet.RowCount)
	for i := range empty {
		empty[i] = true
	}
	chunkRows := sheetRequestCellLimit / sheet.ColumnCount
	if chunkRows > 1000 {
		chunkRows = 1000
	}
	for start := 1; start <= sheet.RowCount; start += chunkRows {
		end := start + chunkRows - 1
		if end > sheet.RowCount {
			end = sheet.RowCount
		}
		cellRange := fmt.Sprintf("A%d:%s%d", start, docsA1Column(sheet.ColumnCount), end)
		result, err := api.ReadRange(ctx, fileID, sheet.SheetID, cellRange)
		if err != nil {
			return fmt.Errorf("查找表格空行失败: %w", err)
		}
		for offset, row := range result.Values {
			if start+offset > end {
				break
			}
			if !sheetRowEmpty(row) {
				empty[start+offset-1] = false
			}
		}
	}

	startRow := firstEmptyRowBlock(empty, len(rows))
	if startRow == 0 {
		return fmt.Errorf("没有足够的连续空行可追加 %d 行数据", len(rows))
	}
	endRow := startRow + len(rows) - 1
	target := fmt.Sprintf("A%d:%s%d", startRow, docsA1Column(cols), endRow)
	check, err := api.ReadRange(ctx, fileID, sheet.SheetID, target)
	if err != nil {
		return fmt.Errorf("写入前检查失败: %w", err)
	}
	for _, row := range check.Values {
		if !sheetRowEmpty(row) {
			return fmt.Errorf("目标区域 %s 已有数据，已取消追加", target)
		}
	}
	if _, err := api.WriteRange(ctx, fileID, sheet.SheetID, target, rows); err != nil {
		return err
	}
	return nil
}

func firstEmptyRowBlock(empty []bool, needed int) int {
	streak := 0
	for i, isEmpty := range empty {
		if isEmpty {
			streak++
			if streak == needed {
				return i - needed + 2 // convert zero-based start to a 1-based row
			}
		} else {
			streak = 0
		}
	}
	return 0
}

func sheetRowEmpty(row []string) bool {
	for _, cell := range row {
		if strings.TrimSpace(cell) != "" {
			return false
		}
	}
	return true
}

func docsA1Column(n int) string {
	if n <= 0 {
		return "A"
	}
	var out []byte
	for n > 0 {
		n--
		out = append([]byte{byte('A' + n%26)}, out...)
		n /= 26
	}
	return string(out)
}

func init() {
	docsSheetWriteCmd.Flags().StringVarP(&docsSheetWriteInput, "input", "i", "", "从文件读取 CSV/TSV 数据")
	docsSheetAddCmd.Flags().IntVar(&docsSheetAddRows, "rows", 200, "初始行数")
	docsSheetAddCmd.Flags().IntVar(&docsSheetAddCols, "cols", 26, "初始列数")
	docsSheetCmd.AddCommand(docsSheetListCmd, docsSheetReadCmd, docsSheetWriteCmd, docsSheetAddCmd, docsSheetRemoveCmd, docsSheetClearCmd, docsSheetDeleteRowsCmd, docsSheetDeleteColsCmd)
}
