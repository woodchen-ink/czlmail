"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import {
  ChevronRight,
  ClipboardPaste,
  Copy,
  CopyPlus,
  Download,
  Eye,
  FilePlus,
  FolderUp,
  Info,
  LayoutGrid,
  List,
  Scissors,
  Star,
  File,
  FileArchive,
  FileImage,
  FileSpreadsheet,
  FileText,
  Folder,
  FolderInput,
  FolderPlus,
  HardDrive,
  Loader2,
  MoreVertical,
  Pencil,
  RefreshCw,
  Search,
  SquareArrowOutUpRight,
  Trash2,
  Upload,
  X,
} from "lucide-react";
import { toast } from "sonner";

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from "@/components/ui/context-menu";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { ScrollArea } from "@/components/ui/scroll-area";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { cn } from "@/lib/utils";
import {
  Events,
  api,
  errorMessage,
  onEvent,
  type Account,
  type FileNode,
  type PIMChange,
} from "@/lib/api";
import { fileSize } from "@/lib/format";
import { onFileDrop } from "@/lib/file-drop";

type Prompt =
  | { kind: "newFolder" }
  | { kind: "newFile" }
  | { kind: "details"; node: FileNode }
  | { kind: "rename"; node: FileNode }
  | { kind: "move"; nodes: FileNode[] }
  | { kind: "delete"; nodes: FileNode[] };

export function FilesShell({ active }: { active: boolean }) {
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [accountId, setAccountId] = useState("");
  const [folderId, setFolderId] = useState("");
  const [path, setPath] = useState<FileNode[]>([]);
  const [nodes, setNodes] = useState<FileNode[]>([]);
  const [query, setQuery] = useState("");
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState("");
  const [prompt, setPrompt] = useState<Prompt | null>(null);
  const [layout, setLayout] = useState<"list" | "grid">(() => readLayout());
  const [favView, setFavView] = useState(false);
  const [favorites, setFavorites] = useState<FileNode[]>([]);
  // 剪切/复制的文件, 粘贴到当前文件夹时执行移动或复制。
  const [clip, setClip] = useState<null | {
    mode: "cut" | "copy";
    accountId: string;
    nodes: FileNode[];
  }>(null);

  const loadFavorites = useCallback(async () => {
    try {
      setFavorites((await api.fileFavorites()) ?? []);
    } catch {
      setFavorites([]);
    }
  }, []);

  useEffect(() => {
    if (active) loadFavorites();
  }, [active, loadFavorites]);

  const favKeys = useMemo(
    () => new Set(favorites.map((f) => `${f.accountId}/${f.id}`)),
    [favorites],
  );

  useEffect(() => {
    if (!active || accountId) return;
    api
      .listAccounts()
      .then((list) => {
        setAccounts(list ?? []);
        const personal = (list ?? []).find((a) => a.isPersonal) ?? list?.[0];
        if (personal) setAccountId(personal.id);
      })
      .catch((err) => toast.error(errorMessage(err)));
  }, [active, accountId]);

  const load = useCallback(async () => {
    if (!accountId) return;
    setLoading(true);
    try {
      const q = query.trim();
      const [list, crumbs] = await Promise.all([
        favView
          ? api.fileFavorites()
          : q
            ? api.searchFiles(accountId, q)
            : api.listFiles(accountId, folderId),
        folderId ? api.filePath(accountId, folderId) : Promise.resolve([]),
      ]);
      setNodes(list ?? []);
      setPath(crumbs ?? []);
      setSelected(
        (prev) =>
          new Set(
            [...prev].filter((id) => (list ?? []).some((n) => n.id === id)),
          ),
      );
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setLoading(false);
    }
  }, [accountId, folderId, query, favView]);

  useEffect(() => {
    if (!active) return;
    const t = setTimeout(load, query ? 150 : 0);
    return () => clearTimeout(t);
  }, [active, load, query]);

  useEffect(() => {
    return onEvent(Events.pimChanged, (c: PIMChange) => {
      if (c.accountId === accountId && (c.type === "" || c.type === "FileNode"))
        load();
    });
  }, [accountId, load]);

  const selectedNodes = useMemo(
    () => nodes.filter((n) => selected.has(n.id)),
    [nodes, selected],
  );

  async function run(key: string, fn: () => Promise<void>) {
    setBusy(key);
    try {
      await fn();
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setBusy("");
    }
  }

  // 文件模块在前台时, 拖入窗口的文件上传到当前文件夹。
  useEffect(() => {
    if (!active) return;
    return onFileDrop((paths) => {
      if (!accountId || favView) return false;
      void run("upload", async () => {
        const n = await api.uploadPaths(accountId, folderId, paths);
        if (n > 0) toast.success(`已上传 ${n} 个项目`);
      });
      return true;
    });
    // run 每次渲染都是新函数, 不列入依赖。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [active, accountId, folderId, favView]);

  async function toggleFavorite(n: FileNode) {
    const on = !favKeys.has(`${n.accountId}/${n.id}`);
    try {
      await api.setFileFavorite(n.accountId, n.id, on);
      await loadFavorites();
      if (favView) load();
    } catch (err) {
      toast.error(errorMessage(err));
    }
  }

  function paste() {
    if (!clip) return;
    if (clip.accountId !== accountId) {
      toast.error("不能跨账号粘贴");
      return;
    }
    const ids = clip.nodes.map((n) => n.id);
    void run("paste", async () => {
      if (clip.mode === "cut") {
        await api.moveFiles(accountId, ids, folderId);
        setClip(null);
        toast.success("已移动");
      } else {
        await api.copyFiles(accountId, ids, folderId);
        toast.success("已复制");
      }
    });
  }

  function openNode(n: FileNode) {
    if (n.isDirectory) {
      setFavView(false);
      if (n.accountId !== accountId) setAccountId(n.accountId);
      setQuery("");
      setFolderId(n.id);
      setSelected(new Set());
      return;
    }
    void run(n.id, async () => {
      const res = await api.openFile(n.accountId, n.id);
      if (res.revealed)
        toast.warning(
          "可执行文件不会直接打开，已在文件夹中选中，请确认来源可信后再运行",
        );
    });
  }

  const upload = () =>
    run("upload", async () => {
      const n = await api.uploadFiles(accountId, folderId);
      if (n > 0) toast.success(`已上传 ${n} 个文件`);
    });

  const uploadFolder = () =>
    run("upload", async () => {
      const n = await api.uploadFolder(accountId, folderId);
      if (n > 0) toast.success(`已上传 ${n} 个项目`);
    });

  const saveAs = (n: FileNode) =>
    run(n.id, async () => {
      const path = await api.saveFileAs(n.accountId, n.id);
      if (path) toast.success(`已保存到 ${path}`);
    });

  function toggle(id: string, on: boolean) {
    setSelected((prev) => {
      const next = new Set(prev);
      if (on) next.add(id);
      else next.delete(id);
      return next;
    });
  }

  const allSelected = nodes.length > 0 && selected.size === nodes.length;

  function changeLayout(l: "list" | "grid") {
    setLayout(l);
    try {
      localStorage.setItem(LAYOUT_KEY, l);
    } catch {
      // 忽略
    }
  }

  /** 右键的是已选中项之一时作用于全部选中项, 否则只作用于它。 */
  const targetsFor = (n: FileNode) =>
    selected.has(n.id) && selectedNodes.length > 1 ? selectedNodes : [n];

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="border-border flex shrink-0 flex-wrap items-center gap-2 border-b px-4 py-2">
        <HardDrive className="text-muted-foreground size-4" />
        {accounts.length > 1 ? (
          <Select
            value={accountId}
            onValueChange={(v) => {
              setAccountId(v);
              setFolderId("");
              setSelected(new Set());
            }}
          >
            <SelectTrigger size="sm" className="w-auto min-w-40">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {accounts.map((a) => (
                <SelectItem key={a.id} value={a.id}>
                  {a.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        ) : (
          <span className="text-sm font-medium">文件</span>
        )}

        <nav
          className="flex min-w-0 flex-1 items-center gap-0.5 text-sm"
          aria-label="路径"
        >
          <Crumb
            label={favView ? "收藏" : "全部文件"}
            onClick={() => {
              setFavView(false);
              setFolderId("");
            }}
            current={!folderId && !query}
          />
          {!query &&
            !favView &&
            path.map((p, i) => (
              <span key={p.id} className="flex min-w-0 items-center gap-0.5">
                <ChevronRight className="text-muted-foreground size-3.5 shrink-0" />
                <Crumb
                  label={p.name}
                  onClick={() => setFolderId(p.id)}
                  current={i === path.length - 1}
                />
              </span>
            ))}
          {query && (
            <>
              <ChevronRight className="text-muted-foreground size-3.5 shrink-0" />
              <span className="text-muted-foreground truncate">
                搜索「{query}」
              </span>
            </>
          )}
        </nav>

        <div className="relative w-56">
          <Search className="text-muted-foreground pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2" />
          <Input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="搜索文件"
            className="h-8 pr-8 pl-8"
          />
          {query && (
            <button
              type="button"
              onClick={() => setQuery("")}
              aria-label="清除搜索"
              className="text-muted-foreground hover:text-foreground absolute top-1/2 right-2 -translate-y-1/2"
            >
              <X className="size-3.5" />
            </button>
          )}
        </div>
        <div className="flex items-center gap-0.5">
          <ToolBtn
            label={layout === "list" ? "网格视图" : "列表视图"}
            onClick={() => changeLayout(layout === "list" ? "grid" : "list")}
          >
            {layout === "list" ? <LayoutGrid /> : <List />}
          </ToolBtn>
          <ToolBtn
            label={favView ? "返回全部文件" : "收藏"}
            active={favView}
            onClick={() => {
              setFavView(!favView);
              setQuery("");
              setSelected(new Set());
            }}
          >
            <Star />
          </ToolBtn>
          <span className="bg-border mx-1 h-5 w-px" />
          <ToolBtn
            label="上传文件"
            onClick={upload}
            disabled={!accountId || busy === "upload" || favView}
          >
            {busy === "upload" ? (
              <Loader2 className="animate-spin" />
            ) : (
              <Upload />
            )}
          </ToolBtn>
          <ToolBtn
            label="上传文件夹"
            onClick={uploadFolder}
            disabled={!accountId || busy === "upload" || favView}
          >
            <FolderUp />
          </ToolBtn>
          <ToolBtn
            label="新建文件夹"
            onClick={() => setPrompt({ kind: "newFolder" })}
            disabled={!accountId || !!query || favView}
          >
            <FolderPlus />
          </ToolBtn>
          <ToolBtn
            label="新建文本文件"
            onClick={() => setPrompt({ kind: "newFile" })}
            disabled={!accountId || !!query || favView}
          >
            <FilePlus />
          </ToolBtn>
          {clip && !favView && !query && (
            <ToolBtn
              label={`粘贴 ${clip.nodes.length} 项到这里`}
              onClick={paste}
              disabled={busy === "paste"}
            >
              <ClipboardPaste />
            </ToolBtn>
          )}
          <ToolBtn label="刷新" onClick={load}>
            {loading ? <Loader2 className="animate-spin" /> : <RefreshCw />}
          </ToolBtn>
        </div>
      </div>

      {selected.size > 0 && (
        <div className="bg-secondary border-border flex shrink-0 items-center gap-2 border-b px-4 py-1.5 text-sm">
          <span>已选 {selected.size} 项</span>
          <Button
            variant="ghost"
            size="sm"
            onClick={() =>
              setClip({ mode: "cut", accountId, nodes: selectedNodes })
            }
          >
            <Scissors className="size-4" />
            剪切
          </Button>
          <Button
            variant="ghost"
            size="sm"
            onClick={() =>
              setClip({ mode: "copy", accountId, nodes: selectedNodes })
            }
          >
            <Copy className="size-4" />
            复制
          </Button>
          <Button
            variant="ghost"
            size="sm"
            onClick={() => setPrompt({ kind: "move", nodes: selectedNodes })}
          >
            <FolderInput className="size-4" />
            移动
          </Button>
          <Button
            variant="ghost"
            size="sm"
            className="text-destructive"
            onClick={() => setPrompt({ kind: "delete", nodes: selectedNodes })}
          >
            <Trash2 className="size-4" />
            删除
          </Button>
          <Button
            variant="ghost"
            size="sm"
            className="ml-auto"
            onClick={() => setSelected(new Set())}
          >
            取消选择
          </Button>
        </div>
      )}

      <div
        className={cn(
          "border-border text-muted-foreground grid shrink-0 grid-cols-[40px_minmax(0,1fr)_110px_170px_40px] items-center border-b px-2 py-1.5 text-xs",
          layout === "grid" && "hidden",
        )}
      >
        <Checkbox
          checked={allSelected}
          onCheckedChange={(v) =>
            setSelected(
              v === true ? new Set(nodes.map((n) => n.id)) : new Set(),
            )
          }
          aria-label="全选"
          className="justify-self-center"
        />
        <span>名称</span>
        <span className="text-right">大小</span>
        <span className="pl-6">修改时间</span>
        <span />
      </div>

      <ScrollArea className="min-h-0 flex-1">
        {nodes.length === 0 && !loading ? (
          <div className="text-muted-foreground flex flex-col items-center justify-center gap-2 py-16 text-sm">
            <Folder className="size-8" />
            {favView
              ? "还没有收藏的文件"
              : query
                ? "没有匹配的文件"
                : "这个文件夹是空的，可以把文件拖到窗口里上传"}
          </div>
        ) : (
          <div
            className={cn(
              layout === "grid" &&
                "grid grid-cols-[repeat(auto-fill,minmax(128px,1fr))] gap-2 p-3",
            )}
          >
            {nodes.map((n) => {
              const Icon = iconFor(n);
              const menuItems = (
                Item: typeof ContextMenuItem | typeof DropdownMenuItem,
                Sep: typeof ContextMenuSeparator,
              ) => (
                <>
                  <Item onSelect={() => openNode(n)}>
                    {n.isDirectory ? (
                      <SquareArrowOutUpRight className="size-4" />
                    ) : (
                      <Eye className="size-4" />
                    )}
                    {n.isDirectory ? "打开" : "预览"}
                  </Item>
                  {!n.isDirectory && (
                    <Item onSelect={() => saveAs(n)}>
                      <Download className="size-4" />
                      下载…
                    </Item>
                  )}
                  <Sep />
                  <Item
                    onSelect={() =>
                      setClip({
                        mode: "cut",
                        accountId: n.accountId,
                        nodes: targetsFor(n),
                      })
                    }
                  >
                    <Scissors className="size-4" />
                    剪切
                  </Item>
                  <Item
                    onSelect={() =>
                      setClip({
                        mode: "copy",
                        accountId: n.accountId,
                        nodes: targetsFor(n),
                      })
                    }
                  >
                    <Copy className="size-4" />
                    复制
                  </Item>
                  {!n.isDirectory && (
                    <Item
                      onSelect={() =>
                        run(n.id, async () => {
                          await api.duplicateFile(n.accountId, n.id);
                          toast.success("已创建副本");
                        })
                      }
                    >
                      <CopyPlus className="size-4" />
                      创建副本
                    </Item>
                  )}
                  <Item onSelect={() => toggleFavorite(n)}>
                    <Star className="size-4" />
                    {favKeys.has(`${n.accountId}/${n.id}`)
                      ? "取消收藏"
                      : "收藏"}
                  </Item>
                  <Item
                    onSelect={() => setPrompt({ kind: "details", node: n })}
                  >
                    <Info className="size-4" />
                    详情
                  </Item>
                  <Sep />
                  <Item onSelect={() => setPrompt({ kind: "rename", node: n })}>
                    <Pencil className="size-4" />
                    重命名
                  </Item>
                  <Item
                    onSelect={() =>
                      setPrompt({ kind: "move", nodes: targetsFor(n) })
                    }
                  >
                    <FolderInput className="size-4" />
                    移动到…
                  </Item>
                  <Sep />
                  <Item
                    variant="destructive"
                    onSelect={() =>
                      setPrompt({ kind: "delete", nodes: targetsFor(n) })
                    }
                  >
                    <Trash2 className="size-4" />
                    删除
                  </Item>
                </>
              );
              if (layout === "grid") {
                const isSel = selected.has(n.id);
                return (
                  <ContextMenu key={n.id}>
                    <ContextMenuTrigger asChild>
                      <div
                        role="button"
                        tabIndex={0}
                        title={n.name}
                        onClick={(e) => {
                          if (e.ctrlKey || e.metaKey || selected.size > 0)
                            toggle(n.id, !isSel);
                        }}
                        onDoubleClick={() => openNode(n)}
                        onKeyDown={(e) => e.key === "Enter" && openNode(n)}
                        className={cn(
                          "group border-border relative flex cursor-default flex-col items-center gap-2 rounded-md border px-2 pt-5 pb-3 text-center text-sm",
                          "hover:bg-secondary focus-visible:ring-ring focus-visible:ring-2 focus-visible:outline-none",
                          isSel && "bg-muted border-accent",
                        )}
                      >
                        <span
                          className={cn(
                            "absolute top-1.5 left-1.5",
                            !isSel &&
                              selected.size === 0 &&
                              "opacity-0 group-hover:opacity-100",
                          )}
                          onClick={(e) => e.stopPropagation()}
                        >
                          <Checkbox
                            checked={isSel}
                            onCheckedChange={(v) => toggle(n.id, v === true)}
                            aria-label={`选择 ${n.name}`}
                          />
                        </span>
                        {favKeys.has(`${n.accountId}/${n.id}`) && (
                          <Star className="text-chart-4 fill-chart-4 absolute top-2 right-2 size-3.5" />
                        )}
                        {busy === n.id ? (
                          <Loader2 className="text-muted-foreground size-10 animate-spin" />
                        ) : (
                          <Icon
                            className={cn(
                              "size-10",
                              n.isDirectory
                                ? "text-folder-archive"
                                : "text-muted-foreground",
                            )}
                            strokeWidth={1.25}
                          />
                        )}
                        <span className="line-clamp-2 w-full text-xs break-all">
                          {n.name}
                        </span>
                        <span className="text-muted-foreground text-[11px] tabular-nums">
                          {n.isDirectory ? "文件夹" : fileSize(n.size) || "0 B"}
                        </span>
                      </div>
                    </ContextMenuTrigger>
                    <ContextMenuContent>
                      {menuItems(ContextMenuItem, ContextMenuSeparator)}
                    </ContextMenuContent>
                  </ContextMenu>
                );
              }
              return (
                <ContextMenu key={n.id}>
                  <ContextMenuTrigger asChild>
                    <div
                      role="button"
                      tabIndex={0}
                      onDoubleClick={() => openNode(n)}
                      onKeyDown={(e) => e.key === "Enter" && openNode(n)}
                      className={cn(
                        "border-border grid cursor-default grid-cols-[40px_minmax(0,1fr)_110px_170px_40px] items-center border-b px-2 py-2 text-sm",
                        "hover:bg-secondary focus-visible:ring-ring focus-visible:ring-2 focus-visible:outline-none",
                        selected.has(n.id) && "bg-muted",
                      )}
                    >
                      <Checkbox
                        checked={selected.has(n.id)}
                        onCheckedChange={(v) => toggle(n.id, v === true)}
                        onClick={(e) => e.stopPropagation()}
                        aria-label={`选择 ${n.name}`}
                        className="justify-self-center"
                      />
                      <button
                        type="button"
                        onClick={() => openNode(n)}
                        className="flex min-w-0 items-center gap-2 text-left hover:underline"
                        title={n.name}
                      >
                        {busy === n.id ? (
                          <Loader2 className="text-muted-foreground size-4 shrink-0 animate-spin" />
                        ) : (
                          <Icon
                            className={cn(
                              "size-4 shrink-0",
                              n.isDirectory
                                ? "text-folder-archive"
                                : "text-muted-foreground",
                            )}
                          />
                        )}
                        <span className="truncate">{n.name}</span>
                        {favKeys.has(`${n.accountId}/${n.id}`) && (
                          <Star className="text-chart-4 fill-chart-4 size-3 shrink-0" />
                        )}
                      </button>
                      <span className="text-muted-foreground text-right text-xs tabular-nums">
                        {n.isDirectory ? "—" : fileSize(n.size) || "0 B"}
                      </span>
                      <span className="text-muted-foreground pl-6 text-xs tabular-nums">
                        {formatTime(n.modified)}
                      </span>
                      <DropdownMenu>
                        <DropdownMenuTrigger asChild>
                          <Button
                            variant="ghost"
                            size="icon"
                            className="size-7"
                            aria-label="更多操作"
                          >
                            <MoreVertical className="size-4" />
                          </Button>
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="end">
                          {menuItems(DropdownMenuItem, DropdownMenuSeparator)}
                        </DropdownMenuContent>
                      </DropdownMenu>
                    </div>
                  </ContextMenuTrigger>
                  <ContextMenuContent>
                    {menuItems(ContextMenuItem, ContextMenuSeparator)}
                  </ContextMenuContent>
                </ContextMenu>
              );
            })}
          </div>
        )}
      </ScrollArea>

      <NamePrompt
        open={
          prompt?.kind === "newFolder" ||
          prompt?.kind === "rename" ||
          prompt?.kind === "newFile"
        }
        title={
          prompt?.kind === "rename"
            ? "重命名"
            : prompt?.kind === "newFile"
              ? "新建文本文件"
              : "新建文件夹"
        }
        initial={
          prompt?.kind === "rename"
            ? prompt.node.name
            : prompt?.kind === "newFile"
              ? "新建文本.txt"
              : ""
        }
        onClose={() => setPrompt(null)}
        onSubmit={async (name) => {
          if (prompt?.kind === "rename")
            await api.renameFile(prompt.node.accountId, prompt.node.id, name);
          else if (prompt?.kind === "newFile")
            await api.createTextFile(accountId, folderId, name);
          else await api.createFolder(accountId, folderId, name);
        }}
      />

      <Dialog
        open={prompt?.kind === "details"}
        onOpenChange={(o) => !o && setPrompt(null)}
      >
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>详情</DialogTitle>
          </DialogHeader>
          {prompt?.kind === "details" && (
            <dl className="grid grid-cols-[88px_1fr] gap-x-3 gap-y-2 text-sm">
              <dt className="text-muted-foreground">名称</dt>
              <dd className="break-all select-text">{prompt.node.name}</dd>
              <dt className="text-muted-foreground">类型</dt>
              <dd>
                {prompt.node.isDirectory
                  ? "文件夹"
                  : prompt.node.contentType || "文件"}
              </dd>
              {!prompt.node.isDirectory && (
                <>
                  <dt className="text-muted-foreground">大小</dt>
                  <dd className="tabular-nums">
                    {fileSize(prompt.node.size) || "0 B"}（
                    {prompt.node.size.toLocaleString()} 字节）
                  </dd>
                </>
              )}
              <dt className="text-muted-foreground">位置</dt>
              <dd className="break-all">
                /{path.map((p) => p.name).join("/")}
              </dd>
              <dt className="text-muted-foreground">修改时间</dt>
              <dd className="tabular-nums">
                {formatTime(prompt.node.modified)}
              </dd>
              <dt className="text-muted-foreground">账号</dt>
              <dd>
                {accounts.find((a) => a.id === prompt.node.accountId)?.name ??
                  prompt.node.accountId}
              </dd>
            </dl>
          )}
        </DialogContent>
      </Dialog>

      <MoveDialog
        nodes={prompt?.kind === "move" ? prompt.nodes : null}
        accountId={accountId}
        onClose={() => setPrompt(null)}
        onMoved={() => setSelected(new Set())}
      />

      <AlertDialog
        open={prompt?.kind === "delete"}
        onOpenChange={(open) => !open && setPrompt(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>删除文件</AlertDialogTitle>
            <AlertDialogDescription>
              {prompt?.kind === "delete" && prompt.nodes.length === 1
                ? `确定删除「${prompt.nodes[0].name}」？`
                : `确定删除选中的 ${prompt?.kind === "delete" ? prompt.nodes.length : 0} 项？`}
              文件夹会连同其中的内容一起删除，无法撤销。
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={() => {
                if (prompt?.kind !== "delete") return;
                const ids = prompt.nodes.map((n) => n.id);
                void run("delete", async () => {
                  await api.deleteFiles(accountId, ids);
                  setSelected(new Set());
                  toast.success("已删除");
                });
              }}
            >
              删除
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

const LAYOUT_KEY = "czlmail.files.layout";

function readLayout(): "list" | "grid" {
  try {
    return localStorage.getItem(LAYOUT_KEY) === "grid" ? "grid" : "list";
  } catch {
    return "list";
  }
}

function ToolBtn({
  label,
  active,
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
    <Button
      variant="ghost"
      size="icon"
      title={label}
      aria-label={label}
      aria-pressed={active}
      disabled={disabled}
      onClick={onClick}
      className={cn(
        "size-8 [&_svg]:size-4",
        active && "bg-muted text-foreground",
      )}
    >
      {children}
    </Button>
  );
}

function Crumb({
  label,
  onClick,
  current,
}: {
  label: string;
  onClick: () => void;
  current: boolean;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "hover:bg-secondary truncate rounded-sm px-1.5 py-0.5",
        current ? "font-medium" : "text-muted-foreground",
      )}
    >
      {label}
    </button>
  );
}

function NamePrompt({
  open,
  title,
  initial,
  onClose,
  onSubmit,
}: {
  open: boolean;
  title: string;
  initial: string;
  onClose: () => void;
  onSubmit: (name: string) => Promise<void>;
}) {
  const [name, setName] = useState(initial);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (open) setName(initial);
  }, [open, initial]);

  async function submit() {
    if (!name.trim()) return;
    setBusy(true);
    try {
      await onSubmit(name.trim());
      onClose();
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-sm">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
        </DialogHeader>
        <Input
          autoFocus
          value={name}
          onChange={(e) => setName(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && submit()}
          onFocus={(e) => {
            // 重命名时只选中扩展名之前的部分。
            const dot = e.currentTarget.value.lastIndexOf(".");
            e.currentTarget.setSelectionRange(
              0,
              dot > 0 ? dot : e.currentTarget.value.length,
            );
          }}
        />
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            取消
          </Button>
          <Button onClick={submit} disabled={busy || !name.trim()}>
            {busy && <Loader2 className="size-4 animate-spin" />}
            确定
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** 选择目标文件夹：逐级浏览目录，不一次性加载整棵树。 */
function MoveDialog({
  nodes,
  accountId,
  onClose,
  onMoved,
}: {
  nodes: FileNode[] | null;
  accountId: string;
  onClose: () => void;
  onMoved: () => void;
}) {
  const [folderId, setFolderId] = useState("");
  const [path, setPath] = useState<FileNode[]>([]);
  const [folders, setFolders] = useState<FileNode[]>([]);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (nodes) setFolderId("");
  }, [nodes]);

  useEffect(() => {
    if (!nodes) return;
    const moving = new Set(nodes.map((n) => n.id));
    Promise.all([
      api.listFiles(accountId, folderId),
      folderId ? api.filePath(accountId, folderId) : Promise.resolve([]),
    ])
      .then(([list, crumbs]) => {
        // 不能把文件夹移进它自己。
        setFolders(
          (list ?? []).filter((n) => n.isDirectory && !moving.has(n.id)),
        );
        setPath(crumbs ?? []);
      })
      .catch((err) => toast.error(errorMessage(err)));
  }, [nodes, accountId, folderId]);

  async function move() {
    if (!nodes) return;
    setBusy(true);
    try {
      await api.moveFiles(
        accountId,
        nodes.map((n) => n.id),
        folderId,
      );
      toast.success("已移动");
      onMoved();
      onClose();
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open={nodes !== null} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>移动到</DialogTitle>
        </DialogHeader>
        <nav className="flex flex-wrap items-center gap-0.5 text-sm">
          <Crumb
            label="全部文件"
            onClick={() => setFolderId("")}
            current={!folderId}
          />
          {path.map((p, i) => (
            <span key={p.id} className="flex items-center gap-0.5">
              <ChevronRight className="text-muted-foreground size-3.5" />
              <Crumb
                label={p.name}
                onClick={() => setFolderId(p.id)}
                current={i === path.length - 1}
              />
            </span>
          ))}
        </nav>
        <div className="border-border h-64 overflow-y-auto rounded-md border">
          {folders.length === 0 ? (
            <p className="text-muted-foreground py-10 text-center text-sm">
              没有子文件夹
            </p>
          ) : (
            folders.map((f) => (
              <button
                key={f.id}
                type="button"
                onClick={() => setFolderId(f.id)}
                className="hover:bg-secondary flex w-full items-center gap-2 px-3 py-2 text-left text-sm"
              >
                <Folder className="text-folder-archive size-4" />
                <span className="truncate">{f.name}</span>
                <ChevronRight className="text-muted-foreground ml-auto size-4" />
              </button>
            ))
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            取消
          </Button>
          <Button onClick={move} disabled={busy}>
            {busy && <Loader2 className="size-4 animate-spin" />}
            移动到这里
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function iconFor(n: FileNode) {
  if (n.isDirectory) return Folder;
  const type = (n.contentType || "").toLowerCase();
  const ext = (n.name.split(".").pop() ?? "").toLowerCase();
  if (type.startsWith("image/")) return FileImage;
  if (
    /zip|rar|7z|tar|gzip/.test(type) ||
    ["zip", "rar", "7z", "tar", "gz"].includes(ext)
  )
    return FileArchive;
  if (/sheet|excel|csv/.test(type) || ["xls", "xlsx", "csv"].includes(ext))
    return FileSpreadsheet;
  if (
    type.startsWith("text/") ||
    type === "application/pdf" ||
    /word|document|json/.test(type)
  )
    return FileText;
  return File;
}

function formatTime(unix: number): string {
  if (!unix) return "";
  const d = new Date(unix * 1000);
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}
