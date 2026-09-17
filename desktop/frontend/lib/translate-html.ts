/**
 * 原地翻译邮件: 只替换 HTML 里的文字节点, 标签、内联样式、图片、链接地址一律不动,
 * 译文按原排版显示。
 *
 * 文字片段按文档顺序编号后分批交给模型, 模型按同样的顺序返回译文数组。
 */

const SKIP_TAGS = new Set(["SCRIPT", "STYLE", "NOSCRIPT", "TITLE", "HEAD", "CODE", "TEXTAREA", "svg"]);

/** 没有字母或文字的片段(纯数字、符号、网址)不必翻译。 */
function worthTranslating(s: string): boolean {
  const t = s.trim();
  if (!t) return false;
  if (/^(https?:\/\/|mailto:|www\.)\S+$/i.test(t)) return false;
  if (/^[\w.+-]+@[\w-]+\.[\w.-]+$/.test(t)) return false;
  return /\p{L}/u.test(t);
}

export interface Segments {
  /** 待翻译的文字(已去掉首尾空白)。 */
  texts: string[];
  /** 用译文重建正文。translations[i] 缺失时保留原文。 */
  render: (translations: (string | undefined)[]) => string;
  isHtml: boolean;
}

export function extractSegments(html: string, text: string): Segments {
  if (!html.trim()) {
    // 纯文本邮件按段落切分, 段落之间的空行原样保留。
    const parts = text.split(/(\n\s*\n)/);
    const idx: number[] = [];
    const texts: string[] = [];
    parts.forEach((p, i) => {
      if (i % 2 === 0 && worthTranslating(p)) {
        idx.push(i);
        texts.push(p.trim());
      }
    });
    return {
      texts,
      isHtml: false,
      render: (tr) => {
        const out = [...parts];
        idx.forEach((pi, k) => {
          if (tr[k] !== undefined) out[pi] = tr[k]!;
        });
        return out.join("");
      },
    };
  }

  const doc = new DOMParser().parseFromString(html, "text/html");
  const nodes: Text[] = [];
  const walker = doc.createTreeWalker(doc.body ?? doc.documentElement, NodeFilter.SHOW_TEXT, {
    acceptNode(node) {
      for (let el = node.parentElement; el; el = el.parentElement) {
        if (SKIP_TAGS.has(el.tagName)) return NodeFilter.FILTER_REJECT;
      }
      return worthTranslating(node.nodeValue ?? "") ? NodeFilter.FILTER_ACCEPT : NodeFilter.FILTER_REJECT;
    },
  });
  for (let n = walker.nextNode(); n; n = walker.nextNode()) nodes.push(n as Text);

  const originals = nodes.map((n) => n.nodeValue ?? "");
  const texts = originals.map((s) => s.trim());
  const doctype = /^\s*<!doctype[^>]*>/i.exec(html)?.[0] ?? "";

  return {
    texts,
    isHtml: true,
    render: (tr) => {
      nodes.forEach((n, i) => {
        const orig = originals[i];
        const t = tr[i];
        if (t === undefined) {
          n.nodeValue = orig;
          return;
        }
        // 保留原片段首尾的空白, 否则相邻行内元素之间的空格会丢。
        const lead = /^\s*/.exec(orig)?.[0] ?? "";
        const trail = /\s*$/.exec(orig)?.[0] ?? "";
        n.nodeValue = lead + t + trail;
      });
      return doctype + doc.documentElement.outerHTML;
    },
  };
}

/** 把片段按字数分批, 单批不超过 maxChars。 */
export function batchSegments(texts: string[], maxChars = 3000, maxItems = 60): number[][] {
  const batches: number[][] = [];
  let cur: number[] = [];
  let size = 0;
  texts.forEach((t, i) => {
    if (cur.length > 0 && (size + t.length > maxChars || cur.length >= maxItems)) {
      batches.push(cur);
      cur = [];
      size = 0;
    }
    cur.push(i);
    size += t.length;
  });
  if (cur.length) batches.push(cur);
  return batches;
}

/** 从模型输出里取出 JSON 字符串数组; 模型偶尔会包一层代码块或多说一句话。 */
export function parseTranslations(output: string): string[] | null {
  const start = output.indexOf("[");
  const end = output.lastIndexOf("]");
  if (start < 0 || end <= start) return null;
  try {
    const arr = JSON.parse(output.slice(start, end + 1));
    return Array.isArray(arr) ? arr.map((v) => (typeof v === "string" ? v : String(v ?? ""))) : null;
  } catch {
    return null;
  }
}

/**
 * 粗略判断文字是否已经是目标语言, 用来决定要不要显示「翻译」按钮。
 * 只对能按文字系统区分的语言下结论(中日韩俄、英文); 同为拉丁字母的德法西等无法可靠区分, 一律返回 false。
 */
export function isAlreadyInLanguage(text: string, language: string): boolean {
  const sample = text.slice(0, 4000);
  const letters = sample.match(/\p{L}/gu)?.length ?? 0;
  if (letters < 20) return false;
  const count = (re: RegExp) => sample.match(re)?.length ?? 0;
  const han = count(/\p{Script=Han}/gu);
  const kana = count(/[\p{Script=Hiragana}\p{Script=Katakana}]/gu);
  const hangul = count(/\p{Script=Hangul}/gu);
  const cyrillic = count(/\p{Script=Cyrillic}/gu);
  const latin = count(/\p{Script=Latin}/gu);
  const lang = language.toLowerCase();

  if (/中文|chinese|zh/.test(lang)) {
    // 英文夹杂的中文邮件(链接、品牌名)很常见, 汉字过四成就算中文; 有假名的是日文。
    // 按"字"与"词"比较: 一个英文单词占好几个字母, 直接比字母数会低估中文的比重。
    const latinWords = count(/[A-Za-z]{2,}/g);
    return han / (han + latinWords * 1.5) > 0.5 && kana / letters < 0.05;
  }
  if (/日本|japanese|ja/.test(lang)) return kana / letters > 0.1;
  if (/한국|korean|ko/.test(lang)) return hangul / letters > 0.4;
  if (/русск|russian|ru/.test(lang)) return cyrillic / letters > 0.5;
  if (/english|英文|英语|en/.test(lang)) {
    return latin / letters > 0.9 && /\b(the|and|to|of|you|your|is|for)\b/i.test(sample);
  }
  return false;
}
