"use client"

import * as React from "react"
import { cn } from "cn"

/**
 * 原生滚动容器。
 *
 * 不用 Radix ScrollArea：它用 JS 模拟滚动条，在嵌套 flex 与动态高度的列表里滑块位置
 * 会算错（顶部时滑块在底部、拖动不跟手）。原生滚动由浏览器负责，永远跟手；
 * 外观由 globals.css 的 .czl-scroll 统一成细滚动条。
 */
function ScrollArea({ className, children, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="scroll-area"
      className={cn("czl-scroll overflow-x-hidden overflow-y-auto overscroll-contain", className)}
      {...props}
    >
      {children}
    </div>
  )
}

export { ScrollArea }
