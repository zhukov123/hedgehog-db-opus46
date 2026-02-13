import { useEffect, useState } from 'react';
import { api, type ClusterStatus as ClusterStatusType, type ClusterNode } from '../lib/api';

const STATUS_LABELS: Record<number, string> = {
  0: 'Alive',
  1: 'Suspect',
  2: 'Dead',
};

const STATUS_COLORS: Record<number, string> = {
  0: 'bg-green-500',
  1: 'bg-yellow-500',
  2: 'bg-red-500',
};

export default function ClusterStatus() {
  const [status, setStatus] = useState<ClusterStatusType | null>(null);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    const load = () => {
      api.clusterStatus()
        .then(setStatus)
        .catch(e => setError(e.message))
        .finally(() => setLoading(false));
    };
    load();
    const interval = setInterval(load, 5000);
    return () => clearInterval(interval);
  }, []);

  if (loading) return <p className="text-gray-500">Loading cluster status...</p>;
  if (error) return <div className="p-4 bg-red-50 text-red-700 rounded-lg">{error}</div>;
  if (!status) return null;

  return (
    <div>
      <h1 className="text-3xl font-bold text-gray-900 mb-8">Cluster Status</h1>

      {/* Ring Visualization */}
      <div className="bg-white rounded-lg shadow p-6 mb-6">
        <h2 className="text-lg font-semibold mb-4">Hash Ring</h2>
        <div className="flex justify-center">
          <RingVisualization nodes={status.nodes || []} />
        </div>
      </div>

      {/* Configuration */}
      <div className="bg-white rounded-lg shadow p-6 mb-6">
        <h2 className="text-lg font-semibold mb-4">Configuration</h2>
        <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
          <ConfigItem label="Node ID" value={status.node_id} />
          <ConfigItem label="Address" value={status.bind_addr} />
          <ConfigItem label="Replication (N)" value={String(status.replication_n)} />
          <ConfigItem label="R + W" value={`${status.read_quorum} + ${status.write_quorum} = ${status.read_quorum + status.write_quorum}`} />
        </div>
        <div className="mt-3 text-sm">
          {status.read_quorum + status.write_quorum > status.replication_n ? (
            <span className="text-green-600 font-medium">Strong consistency (R + W &gt; N)</span>
          ) : (
            <span className="text-yellow-600 font-medium">Eventual consistency (R + W &le; N)</span>
          )}
        </div>
      </div>

      {/* Nodes */}
      <div className="bg-white rounded-lg shadow overflow-hidden">
        <div className="px-6 py-4 border-b">
          <h2 className="text-lg font-semibold">Nodes ({status.total_nodes})</h2>
        </div>
        <table className="w-full">
          <thead className="bg-gray-50">
            <tr>
              <th className="text-left px-6 py-3 text-xs font-medium text-gray-500 uppercase">Status</th>
              <th className="text-left px-6 py-3 text-xs font-medium text-gray-500 uppercase">Node ID</th>
              <th className="text-left px-6 py-3 text-xs font-medium text-gray-500 uppercase">Address</th>
              <th className="text-left px-6 py-3 text-xs font-medium text-gray-500 uppercase">Last Seen</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-gray-200">
            {(status.nodes || []).map((node: ClusterNode) => (
              <tr key={node.id} className="hover:bg-gray-50">
                <td className="px-6 py-4">
                  <span className={`inline-flex items-center gap-2 px-2 py-1 rounded-full text-xs font-medium text-white ${STATUS_COLORS[node.status] || 'bg-gray-400'}`}>
                    {STATUS_LABELS[node.status] || 'Unknown'}
                  </span>
                </td>
                <td className="px-6 py-4 font-mono">{node.id}</td>
                <td className="px-6 py-4 font-mono text-sm">{node.addr}</td>
                <td className="px-6 py-4 text-sm text-gray-500">
                  {new Date(node.last_seen).toLocaleTimeString()}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function ConfigItem({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <span className="text-xs text-gray-500 uppercase">{label}</span>
      <p className="font-mono font-medium">{value}</p>
    </div>
  );
}

function RingVisualization({ nodes }: { nodes: ClusterNode[] }) {
  const size = 200;
  const center = size / 2;
  const radius = 80;

  if (nodes.length === 0) {
    return <p className="text-gray-400">No nodes</p>;
  }

  return (
    <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`}>
      {/* Ring circle */}
      <circle
        cx={center}
        cy={center}
        r={radius}
        fill="none"
        stroke="#e5e7eb"
        strokeWidth="3"
      />

      {/* Node dots */}
      {nodes.map((node, i) => {
        const angle = (2 * Math.PI * i) / nodes.length - Math.PI / 2;
        const x = center + radius * Math.cos(angle);
        const y = center + radius * Math.sin(angle);
        const color = node.status === 0 ? '#22c55e' : node.status === 1 ? '#eab308' : '#ef4444';

        return (
          <g key={node.id}>
            <circle cx={x} cy={y} r={8} fill={color} />
            <text
              x={x}
              y={y + 20}
              textAnchor="middle"
              className="text-[9px] fill-gray-600"
            >
              {node.id}
            </text>
          </g>
        );
      })}

      {/* Center label */}
      <text
        x={center}
        y={center - 4}
        textAnchor="middle"
        className="text-xs font-semibold fill-gray-700"
      >
        {nodes.length}
      </text>
      <text
        x={center}
        y={center + 10}
        textAnchor="middle"
        className="text-[9px] fill-gray-400"
      >
        nodes
      </text>
    </svg>
  );
}
