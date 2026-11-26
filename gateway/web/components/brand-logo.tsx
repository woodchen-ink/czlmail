import { asset } from "@/lib/base-path"
import { cn } from "@/lib/utils"

/** The czlmail logo: the colour version on light surfaces, the white one in dark mode. */
export function BrandLogo({ className }: { className?: string }) {
  return (
    <>
      {/* eslint-disable-next-line @next/next/no-img-element -- static export, no image optimisation */}
      <img
        src={asset("/logo.svg")}
        alt=""
        aria-hidden
        className={cn("shrink-0 dark:hidden", className)}
      />
      {/* eslint-disable-next-line @next/next/no-img-element */}
      <img
        src={asset("/logo-white.svg")}
        alt=""
        aria-hidden
        className={cn("hidden shrink-0 dark:block", className)}
      />
    </>
  )
}
