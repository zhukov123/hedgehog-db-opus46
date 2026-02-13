const BASE = '';

export interface TableMeta {
  name: string;
  created_at: string;
  item_count: number;
}

export interface ClusterNode {
  id: string;
  addr: string;
  status: number;
  last_seen: string;
}

export interface ClusterStatus {
  node_id: string;
  bind_addr: string;
  replication_n: number;
  read_quorum: number;
  write_quorum: number;
  total_nodes: number;
  ring_size: number;
  nodes: ClusterNode[];
}

export interface ItemEntry {
  key: string;
  item: Record<string, unknown>;
}

async function request(method: string, path: string, body?: unknown) {
  const opts: RequestInit = {
    method,
    headers: { 'Content-Type': 'application/json' },
  };
  if (body) {
    opts.body = JSON.stringify(body);
  }
  const resp = await fetch(`${BASE}${path}`, opts);
  const data = await resp.json();
  if (!resp.ok) {
    throw new Error(data.error || `Request failed: ${resp.status}`);
  }
  return data;
}

export const api = {
  // Tables
  async listTables(): Promise<TableMeta[]> {
    const data = await request('GET', '/api/v1/tables');
    return data.tables || [];
  },

  async createTable(name: string): Promise<void> {
    await request('POST', '/api/v1/tables', { name });
  },

  async deleteTable(name: string): Promise<void> {
    await request('DELETE', `/api/v1/tables/${name}`);
  },

  // Items
  async getItem(table: string, key: string): Promise<Record<string, unknown>> {
    const data = await request('GET', `/api/v1/tables/${table}/items/${key}`);
    return data.item;
  },

  async putItem(table: string, key: string, item: Record<string, unknown>): Promise<void> {
    await request('PUT', `/api/v1/tables/${table}/items/${key}`, item);
  },

  async deleteItem(table: string, key: string): Promise<void> {
    await request('DELETE', `/api/v1/tables/${table}/items/${key}`);
  },

  async scanItems(table: string): Promise<ItemEntry[]> {
    const data = await request('GET', `/api/v1/tables/${table}/items`);
    return data.items || [];
  },

  // Cluster
  async clusterStatus(): Promise<ClusterStatus> {
    return request('GET', '/api/v1/cluster/status');
  },

  async clusterNodes(): Promise<ClusterNode[]> {
    const data = await request('GET', '/api/v1/cluster/nodes');
    return data.nodes || [];
  },

  // Health
  async health(): Promise<{ status: string }> {
    return request('GET', '/api/v1/health');
  },
};
