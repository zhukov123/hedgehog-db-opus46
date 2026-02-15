import { useEffect, useState } from 'react';
import { api, type ClusterStatus as ClusterStatusType, type ClusterNode, type ReplicationBacklogResponse } from '../lib/api';

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
  const [backlog, setBacklog] = useState<ReplicationBacklogResponse | null>(null);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(true);
  const [addNodeId, setAddNodeId] = useState('');
  const [addAddr, setAddAddr] = useState('');
  const [addError, setAddError] = useState('');
  const [expandedNode, setExpandedNode] = useState<string | null>(null);

  const load = () => {
    api.clusterStatus()
      .then(setStatus)
      .catch(e => setError(e.message))
      .finally(() => setLoading(false));
    api.getReplicationBacklog()
      .then(setBacklog)
      .catch(() => setBacklog(null));
  };

  useEffect(() => {
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

      {/* Seed nodes (for reference) */}
      <div className="bg-white rounded-lg shadow p-6 mb-6">
        <h2 className="text-lg font-semibold mb-2">What are seed nodes?</h2>
        <p className="text-sm text-gray-600">
          When a node starts, <strong>seed nodes</strong> are the addresses it uses to find the cluster (e.g. <code className="bg-gray-100 px-1 rounded">-seed-nodes 127.0.0.1:8081</code>). Node 1 has no seeds; node 2 seeds with 8081; node 3 seeds with 8081,8082. Once heartbeats are received, the ring shows the real node IDs (node-1, node-2, node-3) only.
        </p>
      </div>

      {/* Replication Backlog */}
      <div className="bg-white rounded-lg shadow p-6 mb-6">
        <h2 className="text-lg font-semibold mb-4">Replication Backlog (Hinted Handoff)</h2>
        {backlog && Object.keys(backlog.pending_per_node || {}).length > 0 ? (
          <div className="space-y-3">
            {Object.entries(backlog.pending_per_node).map(([nodeId, count]) => (
              <div key={nodeId} className="border rounded-lg p-3">
                <div className="flex items-center justify-between">
                  <span className="font-mono font-medium">{nodeId}</span>
                  <span className="text-gray-500">{count} pending hint{count !== 1 ? 's' : ''}</span>
                  <div className="flex gap-2">
                    <button
                      onClick={() => setExpandedNode(expandedNode === nodeId ? null : nodeId)}
                      className="text-sm text-blue-600 hover:text-blue-800"
                    >
                      {expandedNode === nodeId ? 'Hide' : 'Show'} hints
                    </button>
                    <button
                      onClick={async () => {
                        try {
                          await api.replayHints(nodeId);
                          load();
                        } catch (e) {
                          setAddError((e as Error).message);
                        }
                      }}
                      className="text-sm bg-blue-600 text-white px-2 py-1 rounded hover:bg-blue-700"
                    >
                      Replay hints
                    </button>
                  </div>
                </div>
                {expandedNode === nodeId && backlog.hints?.[nodeId] && (
                  <div className="mt-2 text-sm">
                    <ul className="divide-y divide-gray-100">
                      {backlog.hints[nodeId].map((h, i) => (
                        <li key={i} className="py-2 flex gap-4">
                          <span className="font-mono">{h.table_name}/{h.key}</span>
                          <span>{h.op}</span>
                          <span className="text-gray-500">{new Date(h.timestamp).toLocaleString()}</span>
                        </li>
                      ))}
                    </ul>
                  </div>
                )}
              </div>
            ))}
          </div>
        ) : (
          <p className="text-gray-500 text-sm">No pending hints. Hints accumulate when writes fail to reach a down node.</p>
        )}
      </div>

      {/* Add Node */}
      <div className="bg-white rounded-lg shadow p-6 mb-6">
        <h2 className="text-lg font-semibold mb-4">Add Node</h2>
        <form
          onSubmit={async (e) => {
            e.preventDefault();
            setAddError('');
            if (!addNodeId.trim() || !addAddr.trim()) return;
            try {
              await api.addClusterNode(addNodeId.trim(), addAddr.trim());
              setAddNodeId('');
              setAddAddr('');
              load();
            } catch (e) {
              setAddError((e as Error).message);
            }
          }}
          className="flex flex-wrap gap-3 items-end"
        >
          <div>
            <label className="block text-xs text-gray-500 mb-1">Node ID</label>
            <input
              type="text"
              value={addNodeId}
              onChange={e => setAddNodeId(e.target.value)}
              placeholder="node-4"
              className="border rounded px-3 py-2 font-mono text-sm w-32"
            />
          </div>
          <div>
            <label className="block text-xs text-gray-500 mb-1">Address</label>
            <input
              type="text"
              value={addAddr}
              onChange={e => setAddAddr(e.target.value)}
              placeholder="127.0.0.1:8084"
              className="border rounded px-3 py-2 font-mono text-sm w-40"
            />
          </div>
          <button type="submit" className="bg-blue-600 text-white px-4 py-2 rounded hover:bg-blue-700">
            Add node
          </button>
          {addError && <span className="text-red-600 text-sm">{addError}</span>}
        </form>
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
              <th className="text-left px-6 py-3 text-xs font-medium text-gray-500 uppercase">Actions</th>
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
                <td className="px-6 py-4">
                  {node.id !== status.node_id ? (
                    <button
                      onClick={async () => {
                        if (!confirm(`Remove node "${node.id}"? This will rebalance keys.`)) return;
                        try {
                          await api.removeClusterNode(node.id);
                          load();
                        } catch (e) {
                          setAddError((e as Error).message);
                        }
                      }}
                      className="text-red-600 hover:text-red-800 text-sm"
                    >
                      Remove
                    </button>
                  ) : (
                    <span className="text-gray-400 text-sm">(this node)</span>
                  )}
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
