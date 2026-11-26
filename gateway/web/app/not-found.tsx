import Link from "next/link"
import { FileQuestion } from "lucide-react"

import { Button } from "@/components/ui/button"

export default function NotFound() {
  return (
    <main className="flex min-h-svh flex-col items-center justify-center gap-3 px-4 text-center">
      <FileQuestion className="size-8 text-foreground-subtle" />
      <p className="text-sm font-medium">页面不存在 (404)</p>
      <Button asChild variant="outline" size="sm">
        <Link href="/">返回概览</Link>
      </Button>
    </main>
  )
}
