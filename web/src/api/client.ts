import createClient from 'openapi-fetch'
import type { paths } from './schema.gen'

// Typed client for the Syncphony API. Paths and payloads come from
// api/openapi.yaml via `pnpm gen`; never hand-write request types.
export const api = createClient<paths>({ baseUrl: '/api' })
