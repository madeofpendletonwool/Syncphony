import { createFileRoute } from '@tanstack/react-router'
import { AppShell } from '@/components/shell/app-shell'

// The app frame (player dock and nav). /design lives here without signing
// in; everything else is under _authed.
export const Route = createFileRoute('/_app')({
  component: AppShell,
})
