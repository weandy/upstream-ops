import * as React from 'react'
import * as SwitchPrimitive from '@radix-ui/react-switch'

import { cn } from '@/lib/utils'

interface SwitchProps
  extends React.ComponentProps<typeof SwitchPrimitive.Root> {
  checkedChildren?: React.ReactNode
  unCheckedChildren?: React.ReactNode
}

function Switch({
  className,
  checkedChildren,
  unCheckedChildren,
  ...props
}: SwitchProps) {
  const hasLabels =
    checkedChildren !== undefined || unCheckedChildren !== undefined

  if (hasLabels) {
    return (
      <SwitchPrimitive.Root
        data-slot="switch"
        className={cn(
          'group peer relative inline-flex h-[22px] min-w-[54px] shrink-0 cursor-pointer items-center rounded-full shadow-xs transition-colors outline-none select-none focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:cursor-not-allowed disabled:opacity-50 data-[state=checked]:bg-emerald-600 data-[state=unchecked]:bg-muted-foreground/40 dark:data-[state=checked]:bg-emerald-500 dark:data-[state=unchecked]:bg-muted-foreground/45',
          className,
        )}
        {...props}
      >
        <span className="hidden pl-2 pr-[22px] text-[11px] leading-none font-medium whitespace-nowrap text-white group-data-[state=checked]:inline-block">
          {checkedChildren}
        </span>
        <span className="hidden pr-2 pl-[22px] text-[11px] leading-none font-medium whitespace-nowrap text-white group-data-[state=unchecked]:inline-block">
          {unCheckedChildren}
        </span>
        <SwitchPrimitive.Thumb
          data-slot="switch-thumb"
          className="pointer-events-none absolute top-1/2 block size-[18px] -translate-y-1/2 rounded-full bg-white shadow-xs ring-0 transition-all duration-200 data-[state=checked]:left-[calc(100%-20px)] data-[state=unchecked]:left-[2px]"
        />
      </SwitchPrimitive.Root>
    )
  }

  return (
    <SwitchPrimitive.Root
      data-slot="switch"
      className={cn(
        'peer data-[state=checked]:bg-primary data-[state=unchecked]:bg-input focus-visible:border-ring focus-visible:ring-ring/50 dark:data-[state=unchecked]:bg-input/80 inline-flex h-[1.15rem] w-8 shrink-0 items-center rounded-full border border-transparent shadow-xs transition-all outline-none focus-visible:ring-[3px] disabled:cursor-not-allowed disabled:opacity-50',
        className,
      )}
      {...props}
    >
      <SwitchPrimitive.Thumb
        data-slot="switch-thumb"
        className="bg-background dark:data-[state=unchecked]:bg-foreground dark:data-[state=checked]:bg-primary-foreground pointer-events-none block size-4 rounded-full ring-0 transition-transform data-[state=checked]:translate-x-[calc(100%-2px)] data-[state=unchecked]:translate-x-0"
      />
    </SwitchPrimitive.Root>
  )
}

export { Switch }
