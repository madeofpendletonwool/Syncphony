import * as React from "react"
import { cn } from "cn"
import { Slider as SliderPrimitive } from "radix-ui"

// A slim track that thickens while you drag it, like a music app's scrubber.
function Slider({ className, ...props }: React.ComponentProps<typeof SliderPrimitive.Root>) {
  return (
    <SliderPrimitive.Root
      data-slot="slider"
      className={cn(
        "group/slider relative flex w-full touch-none items-center py-2 select-none data-disabled:pointer-events-none",
        className
      )}
      {...props}
    >
      <SliderPrimitive.Track
        data-slot="slider-track"
        className="relative h-1 grow overflow-hidden rounded-full bg-foreground/15 transition-[height] duration-200 group-active/slider:h-2"
      >
        <SliderPrimitive.Range data-slot="slider-range" className="absolute h-full bg-foreground" />
      </SliderPrimitive.Track>
      <SliderPrimitive.Thumb
        data-slot="slider-thumb"
        className="block size-3 rounded-full bg-foreground shadow-md outline-none transition-transform duration-200 group-active/slider:scale-125 focus-visible:ring-4 focus-visible:ring-ring/50 group-data-disabled/slider:hidden"
      />
    </SliderPrimitive.Root>
  )
}

export { Slider }
