import { useEffect, useState } from 'react';
import { api, type ItemEntry } from '../lib/api';

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

  const loadItems = () => {
    setLoading(true);
    api.scanItems(tableName)
      .then(setItems)
      .catch(e => setError(e.message))
      .finally(() => setLoading(false));
  };

  useEffect(loadItems, [tableName]);

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
      <div className="flex items-center gap-4 mb-8">
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
      </div>

      {error && (
        <div className="mb-4 p-3 bg-red-50 text-red-700 rounded-lg">{error}</div>
      )}

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
