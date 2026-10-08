package cmd

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"pkg.blksails.net/bk/internal/docsclient"
)

type sheetDocsAPI struct {
	fakeDocsAPI
	sheets       []docsclient.SheetInfo
	reads        map[string][][]string
	writtenRange string
	writtenRows  [][]string
	appendText   string
}

func (f *sheetDocsAPI) ListSheets(context.Context, string) (*docsclient.ListSheetsResult, error) {
	return &docsclient.ListSheetsResult{Sheets: f.sheets}, nil
}

func (f *sheetDocsAPI) ReadRange(_ context.Context, _, _ string, cellRange string) (*docsclient.ReadRangeResult, error) {
	return &docsclient.ReadRangeResult{Range: cellRange, Values: f.reads[cellRange]}, nil
}

func (f *sheetDocsAPI) WriteRange(_ context.Context, _, _, cellRange string, rows [][]string) (*docsclient.WriteRangeResult, error) {
	f.writtenRange = cellRange
	f.writtenRows = rows
	return &docsclient.WriteRangeResult{UpdatedRows: len(rows), UpdatedCells: len(rows) * len(rows[0])}, nil
}

func (f *sheetDocsAPI) Append(_ context.Context, _ string, text string) error {
	f.appendText = text
	return nil
}

func TestReadDelimitedRowsSupportsCSVAndTSV(t *testing.T) {
	for _, test := range []struct {
		name  string
		input string
		want  [][]string
	}{
		{"csv", "a,b\nc", [][]string{{"a", "b"}, {"c", ""}}},
		{"tsv", "a\tb\nc\td", [][]string{{"a", "b"}, {"c", "d"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := readDelimitedRows(strings.NewReader(test.input))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("rows = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestAppendSheetRowsFindsEmptyBlockAndRechecks(t *testing.T) {
	api := &sheetDocsAPI{
		reads: map[string][][]string{
			"A1:C5": {{"编号", "任务"}, {"A100", "已有"}, {}, {}, {"A101", "后续"}},
			"A3:B4": {{}, {}},
		},
	}
	err := appendSheetRows(context.Background(), api, "file", docsclient.SheetInfo{SheetID: "s1", RowCount: 5, ColumnCount: 3}, [][]string{{"A131", "任务一"}, {"A132", "任务二"}})
	if err != nil {
		t.Fatal(err)
	}
	if api.writtenRange != "A3:B4" {
		t.Fatalf("written range = %q", api.writtenRange)
	}
	if len(api.writtenRows) != 2 {
		t.Fatalf("written rows = %#v", api.writtenRows)
	}
}

func TestAppendSheetRowsCancelsWhenTargetChanged(t *testing.T) {
	api := &sheetDocsAPI{
		reads: map[string][][]string{
			"A1:B3": {{"编号"}, {"A100"}, {}},
			"A3:A3": {{"刚写入的数据"}},
		},
	}
	err := appendSheetRows(context.Background(), api, "file", docsclient.SheetInfo{SheetID: "s1", RowCount: 3, ColumnCount: 2}, [][]string{{"A101"}})
	if err == nil || !strings.Contains(err.Error(), "已取消追加") {
		t.Fatalf("unexpected error: %v", err)
	}
	if api.writtenRange != "" {
		t.Fatalf("write should not run, got %q", api.writtenRange)
	}
}

func TestAppendDocsContentPreservesDocumentAppend(t *testing.T) {
	api := &sheetDocsAPI{}
	item := &docsclient.File{ID: "doc-1", Type: "doc"}
	if err := appendDocsContent(context.Background(), api, item, "内容"); err != nil {
		t.Fatal(err)
	}
	if api.appendText != "内容" {
		t.Fatalf("append text = %q", api.appendText)
	}
}

func TestDocsA1Column(t *testing.T) {
	for input, want := range map[int]string{1: "A", 26: "Z", 27: "AA", 52: "AZ", 53: "BA"} {
		if got := docsA1Column(input); got != want {
			t.Fatalf("docsA1Column(%d) = %q, want %q", input, got, want)
		}
	}
}
