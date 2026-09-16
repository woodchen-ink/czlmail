"use client";

import { useEffect } from "react";
import { EditorContent, useEditor, useEditorState, type Editor } from "@tiptap/react";
import StarterKit from "@tiptap/starter-kit";
import { Color, TextStyle } from "@tiptap/extension-text-style";
import TextAlign from "@tiptap/extension-text-align";
import { Table, TableCell, TableHeader, TableRow } from "@tiptap/extension-table";
import Image from "@tiptap/extension-image";
import Placeholder from "@tiptap/extension-placeholder";
import {
  AlignCenter,
  AlignLeft,
  AlignRight,
  Bold,
  Code,
  Heading1,
  Heading2,
  Italic,
  Link2,
  List,
  ListOrdered,
  Palette,
  Quote,
  Redo2,
  RemoveFormatting,
  Strikethrough,
  Table as TableIcon,
  Underline as UnderlineIcon,
  Undo2,
} from "lucide-react";

import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { cn } from "@/lib/utils";

interface Props {
  content: string;
  placeholder?: string;
  onChange: (html: string) => void;
  onReady?: (editor: Editor) => void;
  className?: string;
  /** 工具条右侧的额外按钮(如 AI)。 */
  toolbarExtra?: React.ReactNode;
}

/**
 * 邮件正文编辑器。
 *
 * 文字颜色给一组固定色板: 邮件是发给别人看的, 收件人的客户端多为白底, 这里的颜色值是邮件内容
 * 本身而不是本应用的界面配色, 因此以内联值写入邮件 HTML。
 */
const TEXT_COLORS = ["#000000", "#444444", "#C0392B", "#D35400", "#B7950B", "#1E8449", "#1F618D", "#7D3C98"];

export function RichEditor({ content, placeholder, onChange, onReady, className, toolbarExtra }: Props) {
  const editor = useEditor({
    immediatelyRender: false,
    extensions: [
      StarterKit.configure({
        link: { openOnClick: false, autolink: true, HTMLAttributes: { rel: "noopener noreferrer" } },
      }),
      TextStyle,
      Color,
      TextAlign.configure({ types: ["heading", "paragraph"] }),
      Table.configure({ resizable: false }),
      TableRow,
      TableHeader,
      TableCell,
      Image.configure({ inline: true, allowBase64: true }),
      Placeholder.configure({ placeholder: placeholder ?? "" }),
    ],
    content,
    editorProps: {
      attributes: { class: "czl-editor min-h-48 outline-none px-4 py-3 text-sm leading-relaxed" },
    },
    onUpdate: ({ editor }) => onChange(editor.getHTML()),
  });

  useEffect(() => {
    if (editor && onReady) onReady(editor);
    // onReady 只在编辑器就绪时调用一次。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [editor]);

  return (
    <div className={cn("flex min-h-0 flex-col", className)}>
      {editor && <Toolbar editor={editor} extra={toolbarExtra} />}
      <EditorContent editor={editor} className="czl-scroll min-h-0 flex-1 overflow-y-auto" />
    </div>
  );
}

function Toolbar({ editor, extra }: { editor: Editor; extra?: React.ReactNode }) {
  const s = useEditorState({
    editor,
    selector: ({ editor: e }) => ({
      bold: e.isActive("bold"),
      italic: e.isActive("italic"),
      underline: e.isActive("underline"),
      strike: e.isActive("strike"),
      h1: e.isActive("heading", { level: 1 }),
      h2: e.isActive("heading", { level: 2 }),
      bullet: e.isActive("bulletList"),
      ordered: e.isActive("orderedList"),
      quote: e.isActive("blockquote"),
      code: e.isActive("codeBlock"),
      left: e.isActive({ textAlign: "left" }),
      center: e.isActive({ textAlign: "center" }),
      right: e.isActive({ textAlign: "right" }),
      link: e.isActive("link"),
      canUndo: e.can().undo(),
      canRedo: e.can().redo(),
    }),
  });

  const chain = () => editor.chain().focus();

  function setLink() {
    const prev = editor.getAttributes("link").href as string | undefined;
    const url = window.prompt("链接地址", prev ?? "https://");
    if (url === null) return;
    if (url.trim() === "" || url.trim() === "https://") {
      chain().extendMarkRange("link").unsetLink().run();
      return;
    }
    chain().extendMarkRange("link").setLink({ href: url.trim() }).run();
  }

  return (
    <div className="border-border flex shrink-0 flex-wrap items-center gap-0.5 border-b px-2 py-1.5">
      <Btn label="加粗" active={s.bold} onClick={() => chain().toggleBold().run()}>
        <Bold />
      </Btn>
      <Btn label="斜体" active={s.italic} onClick={() => chain().toggleItalic().run()}>
        <Italic />
      </Btn>
      <Btn label="下划线" active={s.underline} onClick={() => chain().toggleUnderline().run()}>
        <UnderlineIcon />
      </Btn>
      <Btn label="删除线" active={s.strike} onClick={() => chain().toggleStrike().run()}>
        <Strikethrough />
      </Btn>
      <Popover>
        <PopoverTrigger asChild>
          <button type="button" aria-label="文字颜色" className={btnClass(false)}>
            <Palette />
          </button>
        </PopoverTrigger>
        <PopoverContent className="w-auto p-2" align="start">
          <div className="flex gap-1">
            {TEXT_COLORS.map((c) => (
              <button
                key={c}
                type="button"
                aria-label={c}
                className="border-border size-6 rounded-sm border"
                style={{ background: c }}
                onClick={() => chain().setColor(c).run()}
              />
            ))}
            <button
              type="button"
              className="border-border text-muted-foreground rounded-sm border px-1.5 text-xs"
              onClick={() => chain().unsetColor().run()}
            >
              默认
            </button>
          </div>
        </PopoverContent>
      </Popover>
      <Sep />
      <Btn label="标题 1" active={s.h1} onClick={() => chain().toggleHeading({ level: 1 }).run()}>
        <Heading1 />
      </Btn>
      <Btn label="标题 2" active={s.h2} onClick={() => chain().toggleHeading({ level: 2 }).run()}>
        <Heading2 />
      </Btn>
      <Sep />
      <Btn label="无序列表" active={s.bullet} onClick={() => chain().toggleBulletList().run()}>
        <List />
      </Btn>
      <Btn label="有序列表" active={s.ordered} onClick={() => chain().toggleOrderedList().run()}>
        <ListOrdered />
      </Btn>
      <Btn label="引用" active={s.quote} onClick={() => chain().toggleBlockquote().run()}>
        <Quote />
      </Btn>
      <Btn label="代码块" active={s.code} onClick={() => chain().toggleCodeBlock().run()}>
        <Code />
      </Btn>
      <Sep />
      <Btn label="左对齐" active={s.left} onClick={() => chain().setTextAlign("left").run()}>
        <AlignLeft />
      </Btn>
      <Btn label="居中" active={s.center} onClick={() => chain().setTextAlign("center").run()}>
        <AlignCenter />
      </Btn>
      <Btn label="右对齐" active={s.right} onClick={() => chain().setTextAlign("right").run()}>
        <AlignRight />
      </Btn>
      <Sep />
      <Btn label="链接" active={s.link} onClick={setLink}>
        <Link2 />
      </Btn>
      <Btn label="插入表格" onClick={() => chain().insertTable({ rows: 3, cols: 3, withHeaderRow: true }).run()}>
        <TableIcon />
      </Btn>
      <Btn label="清除格式" onClick={() => chain().unsetAllMarks().clearNodes().run()}>
        <RemoveFormatting />
      </Btn>
      <Sep />
      <Btn label="撤销" disabled={!s.canUndo} onClick={() => chain().undo().run()}>
        <Undo2 />
      </Btn>
      <Btn label="重做" disabled={!s.canRedo} onClick={() => chain().redo().run()}>
        <Redo2 />
      </Btn>
      {extra && <div className="ml-auto flex items-center gap-1">{extra}</div>}
    </div>
  );
}

function btnClass(active: boolean) {
  return cn(
    "text-muted-foreground hover:bg-secondary hover:text-foreground flex size-7 items-center justify-center rounded-sm [&_svg]:size-4",
    "disabled:pointer-events-none disabled:opacity-40",
    active && "bg-muted text-foreground",
  );
}

function Btn({
  label,
  active = false,
  disabled,
  onClick,
  children,
}: {
  label: string;
  active?: boolean;
  disabled?: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button type="button" aria-label={label} aria-pressed={active} disabled={disabled} onClick={onClick} className={btnClass(active)}>
          {children}
        </button>
      </TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  );
}

function Sep() {
  return <span className="bg-border mx-1 h-4 w-px" />;
}
