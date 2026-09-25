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
  /**
   * 同 render, 但把每段文字包进 `<span data-czl-tr="i">`: 之后的译文由
   * email-body 经 postMessage 送进 iframe 原地替换, 不必重建 srcDoc(那会整页重载、闪一下)。
   * 会就地改掉内部的 DOM, **只能调一次**, 且调过之后不要再用 render。
   */
  renderMarked: (translations: (string | undefined)[]) => string;
  isHtml: boolean;
}

/** 文字节点在正文里的标记属性, 与 email-body 的桥接脚本约定一致。 */
export const TR_ATTR = "data-czl-tr";

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
    const render = (tr: (string | undefined)[]) => {
      const out = [...parts];
      idx.forEach((pi, k) => {
        if (tr[k] !== undefined) out[pi] = tr[k]!;
      });
      return out.join("");
    };
    // 纯文本正文是一整块 <pre>, 没有可以挂标记的元素, 只能整篇重渲染。
    return { texts, isHtml: false, render, renderMarked: render };
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

  // 保留原片段首尾的空白, 否则相邻行内元素之间的空格会丢。
  const lead = (i: number) => /^\s*/.exec(originals[i])?.[0] ?? "";
  const trail = (i: number) => /\s*$/.exec(originals[i])?.[0] ?? "";

  return {
    texts,
    isHtml: true,
    render: (tr) => {
      nodes.forEach((n, i) => {
        const t = tr[i];
        n.nodeValue = t === undefined ? originals[i] : lead(i) + t + trail(i);
      });
      return doctype + doc.documentElement.outerHTML;
    },
    renderMarked: (tr) => {
      nodes.forEach((n, i) => {
        const span = doc.createElement("span");
        span.setAttribute(TR_ATTR, String(i));
        span.textContent = tr[i] ?? texts[i];
        n.replaceWith(doc.createTextNode(lead(i)), span, doc.createTextNode(trail(i)));
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

/**
 * 解析模型按编号返回的译文 `{"1":"…","2":"…"}`(编号从 1 起, 与后端送出的一致),
 * 返回长度为 count 的数组, 缺失或损坏的编号为 undefined —— 调用方对这些片段保留原文或重试,
 * 不会因为一处出错丢掉整批, 也不会把译文错位写到别的片段上。
 *
 * 流式时对还没写完的输出调用也行: 只取已经闭合的键值对。先整体 JSON.parse,
 * 不成(没写完、模型包了代码块、把冒号引号写成全角)就从头逐对扫描, 遇到第一处语法错误为止。
 */
export function parseNumberedTranslations(output: string, count: number): (string | undefined)[] {
  const out: (string | undefined)[] = new Array(count);
  const put = (key: string, v: unknown) => {
    const n = Number(key);
    if (typeof v === "string" && Number.isInteger(n) && n >= 1 && n <= count && out[n - 1] === undefined) {
      out[n - 1] = v;
    }
  };
  const start = output.indexOf("{");
  if (start < 0) return out;
  const end = output.lastIndexOf("}");
  if (end > start) {
    try {
      const obj: unknown = JSON.parse(output.slice(start, end + 1));
      if (obj && typeof obj === "object" && !Array.isArray(obj)) {
        for (const [k, v] of Object.entries(obj)) put(k, v);
        return out;
      }
    } catch {
      // 落到下面逐对扫描
    }
  }

  let i = start + 1;
  const ws = () => {
    while (i < output.length && /\s/.test(output[i])) i++;
  };
  // 从 i 处读一个 JSON 字符串; 没闭合或不是字符串时返回 undefined。
  const str = (): string | undefined => {
    if (output[i] !== '"') return undefined;
    const from = i++;
    for (; i < output.length; i++) {
      if (output[i] === "\\") {
        i++;
        continue;
      }
      if (output[i] === '"') {
        i++;
        try {
          const v: unknown = JSON.parse(output.slice(from, i));
          return typeof v === "string" ? v : undefined;
        } catch {
          return undefined;
        }
      }
    }
    return undefined;
  };
  for (;;) {
    ws();
    const key = str();
    if (key === undefined) break;
    ws();
    if (output[i++] !== ":") break;
    ws();
    const v = str();
    if (v === undefined) break;
    put(key, v);
    ws();
    if (output[i] === ",") i++;
    else break;
  }
  return out;
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
