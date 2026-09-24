"use client";

import { Copy, Send } from "lucide-react";
import { toast } from "sonner";

import { SenderAvatar } from "@/components/sender-avatar";
import { Button } from "@/components/ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { errorMessage, type Address } from "@/lib/api";
import { send } from "@/lib/app-bus";
import { displayAddress } from "@/lib/format";

/**
 * 收件人一栏里的一个地址：自己的显示「我」，点开看完整地址并可复制、写信。
 * 名字（尤其是「我」）看不出是哪个邮箱，别名、共享账号多的时候要能随手点开确认。
 */
export function AddressChip({ address, own }: { address: Address; own: boolean }) {
  const email = address.email ?? "";
  const label = own ? "我" : displayAddress(address);

  async function copy() {
    try {
      await navigator.clipboard.writeText(email);
      toast.success("已复制邮箱地址");
    } catch (err) {
      toast.error(errorMessage(err));
    }
  }

  return (
    <Popover>
      <PopoverTrigger asChild>
        <button type="button" className="text-accent hover:underline" title={email}>
          {label}
        </button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-72 p-0">
        <div className="flex items-center gap-3 p-3">
          <SenderAvatar email={email} name={address.name} className="size-10" />
          <div className="min-w-0">
            {address.name && <p className="truncate text-sm font-medium">{address.name}</p>}
            <p className={address.name ? "text-muted-foreground truncate text-xs" : "truncate text-sm font-medium"}>
              {email}
            </p>
          </div>
        </div>
        <div className="border-border flex gap-1 border-t p-1.5">
          <Button variant="ghost" size="sm" className="h-7" onClick={copy}>
            <Copy className="size-3.5" />
            复制
          </Button>
          {!own && (
            <Button variant="ghost" size="sm" className="h-7" onClick={() => send("compose", { to: email })}>
              <Send className="size-3.5" />
              写邮件
            </Button>
          )}
        </div>
      </PopoverContent>
    </Popover>
  );
}
