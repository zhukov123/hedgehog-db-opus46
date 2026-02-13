import { useEffect, useState } from 'react';
import { api, type TableMeta } from '../lib/api';

interface TablesProps {
  onSelectTable: (name: string) => void;
}

export default function Tables({ onSelectTable }: TablesProps) {
  const [tables, setTables] = useState<TableMeta[]>([]);
  const [newName, setNewName] = useState('');
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(true);

  const loadTables = () => {
    setLoading(true);
    api.listTables()
      .then(setTables)
      .catch(e => setError(e.message))
      .finally(() => setLoading(false));
  };

  useEffect(loadTables, []);

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!newName.trim()) return;
    setError('');
    try {
      await api.createTable(newName.trim());
      setNewName('');
      loadTables();
    } catch (err) {
      setError((err as Error).message);
    }
  };

  const handleDelete = async (name: string) => {
    if (!confirm(`Delete table "${name}"?`)) return;
    try {
      await api.deleteTable(name);
      loadTables();
    } catch (err) {
      setError((err as Error).message);
    }
  };

  return (
    <div>
      <h1 className="text-3xl font-bold text-gray-900 mb-8">Tables</h1>

      <form onSubmit={handleCreate} className="flex gap-3 mb-6">
        <input
          type="text"
          value={newName}
          onChange={e => setNewName(e.target.value)}
          placeholder="New table name..."
          className="flex-1 px-4 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500"
        />
        <button
          type="submit"
          className="px-6 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 transition-colors"
        >
          Create Table
        </button>
      </form>

      {error && (
        <div className="mb-4 p-3 bg-red-50 text-red-700 rounded-lg">{error}</div>
      )}

      {loading ? (
        <p className="text-gray-500">Loading...</p>
      ) : tables.length === 0 ? (
        <div className="text-center py-12 text-gray-400">
          <p className="text-lg">No tables yet</p>
          <p className="text-sm mt-1">Create your first table above</p>
        </div>
      ) : (
        <div className="bg-white rounded-lg shadow overflow-hidden">
          <table className="w-full">
            <thead className="bg-gray-50">
              <tr>
                <th className="text-left px-6 py-3 text-xs font-medium text-gray-500 uppercase">Name</th>
                <th className="text-left px-6 py-3 text-xs font-medium text-gray-500 uppercase">Created</th>
                <th className="text-right px-6 py-3 text-xs font-medium text-gray-500 uppercase">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-200">
              {tables.map(t => (
                <tr key={t.name} className="hover:bg-gray-50">
                  <td className="px-6 py-4">
                    <button
                      onClick={() => onSelectTable(t.name)}
                      className="font-mono font-medium text-blue-600 hover:text-blue-800"
                    >
                      {t.name}
                    </button>
                  </td>
                  <td className="px-6 py-4 text-sm text-gray-500">
                    {new Date(t.created_at).toLocaleString()}
                  </td>
                  <td className="px-6 py-4 text-right">
                    <button
                      onClick={() => handleDelete(t.name)}
                      className="text-red-600 hover:text-red-800 text-sm"
                    >
                      Delete
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
