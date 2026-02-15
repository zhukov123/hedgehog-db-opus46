import { useEffect, useState } from 'react';
import { api, type ItemEntry, type ReplicaNodesResponse } from '../lib/api';

interface TableDetailProps {
  tableName: string;
  onBack: () => void;
}

export default function TableDetail({ tableName, onBack }: TableDetailProps) {
  const [items, setItems] = useState<ItemEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  // New item form
  const [newKey, setNewKey] = useState('');
  const [newValue, setNewValue] = useState('{\n  \n}');
  const [editing, setEditing] = useState<string | null>(null);
  const [editValue, setEditValue] = useState('');

  // Replica nodes debug
  const [replicaKey, setReplicaKey] = useState('');
  const [replicaResult, setReplicaResult] = useState<ReplicaNodesResponse | null>(null);
  const [replicaLoading, setReplicaLoading] = useState(false);
  const [clusterNodeId, setClusterNodeId] = useState<string | null>(null);

  useEffect(() => {
    api.clusterStatus().then(s => setClusterNodeId(s.node_id)).catch(() => {});
  }, []);

  const loadItems = (showLoading = true) => {
    if (showLoading) setLoading(true);
    // Use cluster scan when multiple nodes so item count matches trafficgen (full cluster view)
    api.clusterStatus()
      .then(status =>
        status.total_nodes > 1
          ? api.scanItemsCluster(tableName)
          : api.scanItems(tableName)
      )
      .then(setItems)
      .catch(e => setError(e.message))
      .finally(() => setLoading(false));
  };

  useEffect(() => {
    loadItems();
  }, [tableName]);

  // Auto-refresh so new keys from trafficgen (or other clients) appear
  useEffect(() => {
    const interval = setInterval(() => loadItems(false), 3000);
    return () => clearInterval(interval);
  }, [tableName]);

  const handlePut = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!newKey.trim()) return;
    setError('');
    try {
      const doc = JSON.parse(newValue);
      await api.putItem(tableName, newKey.trim(), doc);
      setNewKey('');
      setNewValue('{\n  \n}');
      loadItems();
    } catch (err) {
      setError((err as Error).message);
    }
  };

  const handleDelete = async (key: string) => {
    if (!confirm(`Delete item "${key}"?`)) return;
    try {
      await api.deleteItem(tableName, key);
      loadItems();
    } catch (err) {
      setError((err as Error).message);
    }
  };

  const handleEdit = (entry: ItemEntry) => {
    setEditing(entry.key);
    setEditValue(JSON.stringify(entry.item, null, 2));
  };

  const handleSaveEdit = async () => {
    if (!editing) return;
    setError('');
    try {
      const doc = JSON.parse(editValue);
      await api.putItem(tableName, editing, doc);
      setEditing(null);
      loadItems();
    } catch (err) {
      setError((err as Error).message);
    }
  };

  return (
    <div>
      <div className="flex flex-wrap items-center gap-4 mb-8">
        <button
          onClick={onBack}
          className="text-gray-500 hover:text-gray-700"
        >
          &larr; Back
        </button>
        <h1 className="text-3xl font-bold text-gray-900">
          <span className="font-mono">{tableName}</span>
        </h1>
        <span className="text-sm text-gray-400">{items.length} items</span>
        <button
          type="button"
          onClick={() => loadItems()}
          className="shrink-0 px-4 py-2 rounded-lg bg-blue-600 text-white text-sm font-medium hover:bg-blue-700"
        >
          Refresh
        </button>
      </div>

      {error && (
        <div className="mb-4 p-3 bg-red-50 text-red-700 rounded-lg">{error}</div>
      )}

      {/* Replica Nodes Debug */}
      <div className="bg-white rounded-lg shadow p-6 mb-6 border-l-4 border-amber-400">
        <h2 className="text-lg font-semibold mb-2">Replica Nodes (Debug)</h2>
        <p className="text-sm text-gray-500 mb-3">See which nodes hold replicas of a key</p>
        <div className="flex flex-wrap gap-3 items-center">
          <input
            type="text"
            value={replicaKey}
            onChange={e => setReplicaKey(e.target.value)}
            placeholder="Item key..."
            className="px-3 py-2 border rounded font-mono text-sm w-48"
          />
          <button
            type="button"
            onClick={async () => {
              if (!replicaKey.trim()) return;
              setReplicaLoading(true);
              setReplicaResult(null);
              try {
                const res = await api.getReplicaNodes(replicaKey.trim());
                setReplicaResult(res);
              } catch (e) {
                setError((e as Error).message);
              } finally {
                setReplicaLoading(false);
              }
            }}
            disabled={replicaLoading || !replicaKey.trim()}
            className="px-4 py-2 bg-amber-600 text-white rounded hover:bg-amber-700 disabled:opacity-50 text-sm"
          >
            {replicaLoading ? 'Loading...' : 'Show replicas'}
          </button>
          {replicaResult && (
            <div className="flex flex-wrap gap-4 items-center text-sm">
              <span>
                <span className="text-gray-500">Primary:</span>{' '}
                <span className={`font-mono ${replicaResult.primary === clusterNodeId ? 'text-blue-600 font-medium' : ''}`}>
                  {replicaResult.primary || '(none)'}
                  {replicaResult.primary === clusterNodeId && ' (this node)'}
                </span>
              </span>
              <span>
                <span className="text-gray-500">Replicas:</span>{' '}
                <span className="font-mono">
                  {replicaResult.replicas.length > 0
                    ? replicaResult.replicas.map(r => (
                        <span key={r} className={r === clusterNodeId ? 'text-blue-600 font-medium' : ''}>
                          {r}{r === clusterNodeId ? ' (this node)' : ''}{' '}
                        </span>
                      ))
                    : '(none)'}
                </span>
              </span>
            </div>
          )}
        </div>
      </div>

      {/* Add Item Form */}
      <div className="bg-white rounded-lg shadow p-6 mb-6">
        <h2 className="text-lg font-semibold mb-4">Add Item</h2>
        <form onSubmit={handlePut} className="space-y-3">
          <input
            type="text"
            value={newKey}
            onChange={e => setNewKey(e.target.value)}
            placeholder="Item key..."
            className="w-full px-4 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 font-mono"
          />
          <textarea
            value={newValue}
            onChange={e => setNewValue(e.target.value)}
            rows={4}
            className="w-full px-4 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 font-mono text-sm"
          />
          <button
            type="submit"
            className="px-6 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 transition-colors"
          >
            Put Item
          </button>
        </form>
      </div>

      {/* Edit Modal */}
      {editing && (
        <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50 p-4">
          <div className="bg-white rounded-lg shadow-xl p-6 w-full max-w-lg">
            <h3 className="text-lg font-semibold mb-4">
              Edit <span className="font-mono">{editing}</span>
            </h3>
            <textarea
              value={editValue}
              onChange={e => setEditValue(e.target.value)}
              rows={10}
              className="w-full px-4 py-2 border border-gray-300 rounded-lg font-mono text-sm mb-4"
            />
            <div className="flex gap-3 justify-end">
              <button
                onClick={() => setEditing(null)}
                className="px-4 py-2 text-gray-600 hover:text-gray-800"
              >
                Cancel
              </button>
              <button
                onClick={handleSaveEdit}
                className="px-6 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700"
              >
                Save
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Items List */}
      {loading ? (
        <p className="text-gray-500">Loading...</p>
      ) : items.length === 0 ? (
        <div className="text-center py-12 text-gray-400">
          <p className="text-lg">No items</p>
          <p className="text-sm mt-1">Add your first item above</p>
        </div>
      ) : (
        <div className="space-y-3">
          {items.map(entry => (
            <div key={entry.key} className="bg-white rounded-lg shadow p-4">
              <div className="flex justify-between items-start mb-2">
                <span className="font-mono font-medium text-blue-600">{entry.key}</span>
                <div className="flex gap-2">
                  <button
                    onClick={async () => {
                      setReplicaKey(entry.key);
                      setReplicaLoading(true);
                      setReplicaResult(null);
                      try {
                        const res = await api.getReplicaNodes(entry.key);
                        setReplicaResult(res);
                      } catch (e) {
                        setError((e as Error).message);
                      } finally {
                        setReplicaLoading(false);
                      }
                    }}
                    className="text-sm text-gray-500 hover:text-amber-600"
                    title="Look up replica nodes for this key"
                  >
                    Replicas
                  </button>
                  <button
                    onClick={() => handleEdit(entry)}
                    className="text-sm text-gray-500 hover:text-blue-600"
                  >
                    Edit
                  </button>
                  <button
                    onClick={() => handleDelete(entry.key)}
                    className="text-sm text-gray-500 hover:text-red-600"
                  >
                    Delete
                  </button>
                </div>
              </div>
              <pre className="text-sm bg-gray-50 p-3 rounded overflow-x-auto font-mono">
                {JSON.stringify(entry.item, null, 2)}
              </pre>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
