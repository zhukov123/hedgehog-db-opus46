import { useEffect, useState } from 'react';
import { api, type TableMeta, type ClusterStatus } from '../lib/api';

export default function Dashboard() {
  const [tables, setTables] = useState<TableMeta[]>([]);
  const [cluster, setCluster] = useState<ClusterStatus | null>(null);
  const [healthy, setHealthy] = useState<boolean | null>(null);
  const [tableCounts, setTableCounts] = useState<Record<string, number>>({});
  const [totalItems, setTotalItems] = useState<number | null>(null);

  useEffect(() => {
    api.listTables().then(setTables).catch(() => {});
    api.clusterStatus().then(setCluster).catch(() => {});
    api.health().then(() => setHealthy(true)).catch(() => setHealthy(false));
  }, []);

  useEffect(() => {
    if (tables.length === 0) {
      setTableCounts({});
      setTotalItems(0);
      return;
    }
    let cancelled = false;
    const counts: Record<string, number> = {};
    Promise.all(
      tables.map(t =>
        api.getTableCount(t.name).then(c => {
          if (!cancelled) counts[t.name] = c;
        })
      )
    ).then(() => {
      if (!cancelled) {
        setTableCounts(counts);
        setTotalItems(Object.values(counts).reduce((a, b) => a + b, 0));
      }
    });
    return () => { cancelled = true; };
  }, [tables]);

  const aliveNodes = cluster?.nodes?.filter(n => n.status === 0).length ?? 0;

  return (
    <div>
      <h1 className="text-3xl font-bold text-gray-900 mb-8">Dashboard</h1>

      <div className="grid grid-cols-1 md:grid-cols-4 gap-6 mb-8">
        <StatCard
          label="Tables"
          value={tables.length}
          color="bg-blue-500"
        />
        <StatCard
          label="Total Items"
          value={totalItems === null ? '...' : totalItems}
          color="bg-amber-500"
        />
        <StatCard
          label="Cluster Nodes"
          value={`${aliveNodes} / ${cluster?.total_nodes ?? 0}`}
          color="bg-green-500"
        />
        <StatCard
          label="Health"
          value={healthy === null ? '...' : healthy ? 'OK' : 'DOWN'}
          color={healthy ? 'bg-emerald-500' : 'bg-red-500'}
        />
      </div>

      {cluster && (
        <div className="bg-white rounded-lg shadow p-6 mb-6">
          <h2 className="text-lg font-semibold mb-4">Cluster Configuration</h2>
          <div className="grid grid-cols-2 md:grid-cols-4 gap-4 text-sm">
            <div>
              <span className="text-gray-500">Node ID:</span>
              <p className="font-mono font-medium">{cluster.node_id}</p>
            </div>
            <div>
              <span className="text-gray-500">Bind Address:</span>
              <p className="font-mono font-medium">{cluster.bind_addr}</p>
            </div>
            <div>
              <span className="text-gray-500">Replication:</span>
              <p className="font-medium">N={cluster.replication_n} R={cluster.read_quorum} W={cluster.write_quorum}</p>
            </div>
            <div>
              <span className="text-gray-500">Ring Size:</span>
              <p className="font-medium">{cluster.ring_size} nodes</p>
            </div>
          </div>
        </div>
      )}

      {tables.length > 0 && (
        <div className="bg-white rounded-lg shadow p-6">
          <h2 className="text-lg font-semibold mb-4">Tables</h2>
          <div className="space-y-2">
            {tables.map(t => (
              <div key={t.name} className="flex justify-between items-center p-3 bg-gray-50 rounded">
                <span className="font-mono font-medium">{t.name}</span>
                <div className="flex items-center gap-4">
                  <span className="text-sm font-medium">
                    {tableCounts[t.name] !== undefined ? (
                      <span title="Live count (B+ tree)">{tableCounts[t.name]} items</span>
                    ) : (
                      <span className="text-gray-400">…</span>
                    )}
                  </span>
                  <span className="text-sm text-gray-500">
                    Created: {new Date(t.created_at).toLocaleDateString()}
                  </span>
                </div>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

function StatCard({ label, value, color }: { label: string; value: string | number; color: string }) {
  return (
    <div className="bg-white rounded-lg shadow p-6">
      <div className="flex items-center">
        <div className={`w-3 h-3 rounded-full ${color} mr-3`} />
        <span className="text-sm text-gray-500">{label}</span>
      </div>
      <p className="text-2xl font-bold mt-2">{value}</p>
    </div>
  );
}
