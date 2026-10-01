// 表格文本解析：CSV 文件与从电子表格（Excel / Google Sheets / Numbers）复制出来的
// TSV 文本共用。支持双引号包裹的字段（字段内可含分隔符、换行，"" 表示一个引号），
// 自动去掉 UTF-8 BOM，忽略全空行。分隔符按表头行自动判断：含 Tab 用 Tab，否则用逗号。

export interface ParsedTable {
  delimiter: ',' | '\t';
  header: string[];
  // 每行附带原始行号（从 1 开始，表头是第 1 行），用于逐行报错
  rows: Array<{ line: number; cells: string[] }>;
}

// Excel 导出的 UTF-8 CSV 常带 BOM（U+FEFF）
function stripBom(text: string): string {
  return text.charCodeAt(0) === 0xfeff ? text.slice(1) : text;
}

export function detectDelimiter(text: string): ',' | '\t' {
  const firstLine = stripBom(text).split(/\r?\n/, 1)[0] ?? '';
  return firstLine.includes('\t') ? '\t' : ',';
}

export function parseDelimited(text: string, delimiter: ',' | '\t' = detectDelimiter(text)): ParsedTable {
  const src = stripBom(text);
  const records: Array<{ line: number; cells: string[] }> = [];
  let cells: string[] = [];
  let field = '';
  let inQuotes = false;
  let line = 1;
  let recordLine = 1;

  const endField = () => {
    cells.push(field);
    field = '';
  };
  const endRecord = () => {
    endField();
    if (cells.some((c) => c.trim() !== '')) records.push({ line: recordLine, cells });
    cells = [];
  };

  for (let i = 0; i < src.length; i++) {
    const ch = src[i];
    if (inQuotes) {
      if (ch === '"') {
        if (src[i + 1] === '"') {
          field += '"';
          i++;
        } else {
          inQuotes = false;
        }
      } else {
        if (ch === '\n') line++;
        field += ch;
      }
      continue;
    }
    if (ch === '"' && field.trim() === '') {
      field = '';
      inQuotes = true;
    } else if (ch === delimiter) {
      endField();
    } else if (ch === '\r') {
      // \r\n 由 \n 处理；单独的 \r 也视为换行
      if (src[i + 1] !== '\n') {
        endRecord();
        line++;
        recordLine = line;
      }
    } else if (ch === '\n') {
      endRecord();
      line++;
      recordLine = line;
    } else {
      field += ch;
    }
  }
  if (field !== '' || cells.length > 0) endRecord();

  const [head, ...rest] = records;
  return {
    delimiter,
    header: head ? head.cells.map((h) => h.trim()) : [],
    rows: rest,
  };
}
