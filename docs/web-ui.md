# Web UI

## Tech Stack

- **React 19** with TypeScript
- **Vite 7** for build/dev server
- **Tailwind CSS 4** via `@tailwindcss/vite` plugin
- No React Router — simple state-based page switching

## Project Structure

```
web/
├── src/
│   ├── main.tsx              # React entry point
│   ├── App.tsx               # Main app with navigation and page routing
│   ├── index.css             # Tailwind import
│   ├── lib/
│   │   └── api.ts            # API client (fetch wrapper + typed functions)
│   └── pages/
│       ├── Dashboard.tsx     # Overview: stats, cluster config, table list
│       ├── Tables.tsx        # Table list with create/delete
│       ├── TableDetail.tsx   # Item CRUD with JSON editor
│       └── ClusterStatus.tsx # Cluster nodes, ring visualization, config
├── vite.config.ts            # Vite config with Tailwind plugin + API proxy
├── tsconfig.json             # TypeScript config
└── package.json
```

## Pages

### Dashboard (`Dashboard.tsx`)

Displays on load. Shows:
- **Stat cards**: table count, alive/total nodes, health status
- **Cluster configuration**: node ID, bind address, N/R/W settings, ring size
- **Table list**: name + creation date for each table

Data fetched from: `GET /api/v1/tables`, `GET /api/v1/cluster/status`, `GET /api/v1/health`

### Tables (`Tables.tsx`)

Table management page:
- **Create form**: text input + "Create Table" button
- **Table list**: clickable table names → navigate to TableDetail
- **Delete button**: with confirmation dialog
- Error display for failed operations

Props:
- `onSelectTable(name: string)` — callback to navigate to TableDetail

### TableDetail (`TableDetail.tsx`)

Item CRUD for a single table:
- **Add Item form**: key input + JSON textarea + "Put Item" button
- **Item list**: each item shows key, formatted JSON, Edit/Delete buttons
- **Edit modal**: overlay with JSON textarea for editing an existing item
- **Back button**: returns to Tables page

Props:
- `tableName: string` — which table to display
- `onBack()` — callback to return to Tables list

Data fetched from: `GET /api/v1/tables/{name}/items` (scan)

### ClusterStatus (`ClusterStatus.tsx`)

Cluster monitoring:
- **Ring visualization**: SVG circle with colored dots for each node (green=alive, yellow=suspect, red=dead)
- **Configuration panel**: node ID, address, N/R/W, strong/eventual consistency indicator
- **Node table**: status badge, node ID, address, last seen time
- **Auto-refresh**: polls every 5 seconds

Data fetched from: `GET /api/v1/cluster/status`

## API Client (`lib/api.ts`)

Typed fetch wrapper with these methods:

```typescript
api.listTables(): Promise<TableMeta[]>
api.createTable(name): Promise<void>
api.deleteTable(name): Promise<void>
api.getItem(table, key): Promise<Record<string, unknown>>
api.putItem(table, key, item): Promise<void>
api.deleteItem(table, key): Promise<void>
api.scanItems(table): Promise<ItemEntry[]>
api.clusterStatus(): Promise<ClusterStatus>
api.clusterNodes(): Promise<ClusterNode[]>
api.health(): Promise<{status: string}>
```

All methods throw on non-2xx responses with the error message from the server.

## Navigation

State-based routing in `App.tsx`:

```typescript
type Page = 'dashboard' | 'tables' | 'table-detail' | 'cluster';
```

Navigation via header buttons that call `setPage()`. No URL routing — the entire app is a single-page application with component switching.

## Development

```bash
cd web
npm install
npm run dev     # Start dev server with hot reload (port 5173)
npm run build   # Production build to web/dist/
npx tsc --noEmit  # Type-check without building
```

### API Proxy

In development, Vite proxies `/api/*` requests to the Go backend. Configured in `vite.config.ts`:

```typescript
server: {
  proxy: {
    '/api': 'http://127.0.0.1:8081',  // or wherever the backend is running
  },
}
```

Change the proxy target if your backend is on a different port.

### Production Embedding

The built web UI (`web/dist/`) is embedded into the Go binary via `go:embed`:

```go
// webui.go (project root)
//go:embed all:web/dist
var WebDist embed.FS
```

Served by `internal/api/webui.go:RegisterWebUI()` which:
1. Serves `/assets/*` as static files from the embedded FS
2. Serves `/` with `index.html` (SPA fallback)
3. API routes (`/api/*`, `/internal/*`) are NOT intercepted

### Build for Embedding

```bash
cd web && npm run build   # creates web/dist/
cd .. && make build       # embeds web/dist/ into the Go binary
```

## Styling

All styling uses Tailwind CSS utility classes. No custom CSS files. The design uses:
- Gray-100 background
- White cards with shadow
- Blue accents for interactive elements
- Green/yellow/red for status indicators
- Monospace font for keys, addresses, and JSON
