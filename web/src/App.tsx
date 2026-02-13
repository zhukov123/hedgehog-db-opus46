import { useState } from 'react';
import Dashboard from './pages/Dashboard';
import Tables from './pages/Tables';
import TableDetail from './pages/TableDetail';
import ClusterStatus from './pages/ClusterStatus';

type Page = 'dashboard' | 'tables' | 'table-detail' | 'cluster';

function App() {
  const [page, setPage] = useState<Page>('dashboard');
  const [selectedTable, setSelectedTable] = useState('');

  const handleSelectTable = (name: string) => {
    setSelectedTable(name);
    setPage('table-detail');
  };

  const navItems: { key: Page; label: string }[] = [
    { key: 'dashboard', label: 'Dashboard' },
    { key: 'tables', label: 'Tables' },
    { key: 'cluster', label: 'Cluster' },
  ];

  return (
    <div className="min-h-screen bg-gray-100">
      {/* Header */}
      <header className="bg-white shadow-sm border-b">
        <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8">
          <div className="flex items-center justify-between h-16">
            <div className="flex items-center gap-3">
              <span className="text-2xl">🦔</span>
              <h1 className="text-xl font-bold text-gray-900">HedgehogDB</h1>
            </div>
            <nav className="flex gap-1">
              {navItems.map(item => (
                <button
                  key={item.key}
                  onClick={() => setPage(item.key)}
                  className={`px-4 py-2 rounded-lg text-sm font-medium transition-colors ${
                    page === item.key || (item.key === 'tables' && page === 'table-detail')
                      ? 'bg-blue-50 text-blue-700'
                      : 'text-gray-600 hover:text-gray-900 hover:bg-gray-50'
                  }`}
                >
                  {item.label}
                </button>
              ))}
            </nav>
          </div>
        </div>
      </header>

      {/* Main Content */}
      <main className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-8">
        {page === 'dashboard' && <Dashboard />}
        {page === 'tables' && <Tables onSelectTable={handleSelectTable} />}
        {page === 'table-detail' && (
          <TableDetail
            tableName={selectedTable}
            onBack={() => setPage('tables')}
          />
        )}
        {page === 'cluster' && <ClusterStatus />}
      </main>
    </div>
  );
}

export default App;
