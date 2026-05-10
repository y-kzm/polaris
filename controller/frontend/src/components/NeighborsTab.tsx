import { useState } from 'react';
import { RefreshCw } from 'lucide-react';
import type { Neighbor, Group } from '../types';
import { formatDateTime, neighborLedStatus } from '../utils/format';
import StatusLed from './StatusLed';

interface NeighborsTabProps {
  neighbors: Neighbor[];
  groups: Group[];
  sourceDescription: string;
  onRefresh: () => void;
  isRefreshing: boolean;
  refreshCooldown: number; // seconds until the button re-enables
}

export default function NeighborsTab({ neighbors, groups, sourceDescription, onRefresh, isRefreshing, refreshCooldown }: NeighborsTabProps) {
  const [currentPage, setCurrentPage] = useState(1);
  const [itemsPerPage, setItemsPerPage] = useState(20);

  const getNeighborGroups = (neighborId: string) =>
    groups.filter(g => g.members.includes(neighborId));

  const totalPages = Math.ceil(neighbors.length / itemsPerPage);
  const startIndex = (currentPage - 1) * itemsPerPage;
  const paginatedNeighbors = neighbors.slice(startIndex, startIndex + itemsPerPage);

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <div>
          <h2 className="text-base font-semibold text-gray-800">Clients</h2>
          <p className="text-xs text-gray-500 mt-0.5">Source: {sourceDescription}</p>
        </div>
        <div className="flex items-center gap-3">
          <div className="flex items-center gap-2">
            <label className="text-sm text-gray-500">Show:</label>
            <select
              value={itemsPerPage}
              onChange={e => { setItemsPerPage(Number(e.target.value)); setCurrentPage(1); }}
              className="px-2 py-1 border rounded text-sm"
            >
              {[10, 20, 50, 100].map(n => <option key={n} value={n}>{n}</option>)}
            </select>
            <span className="text-sm text-gray-500">per page</span>
          </div>
          <button
            onClick={onRefresh}
            disabled={isRefreshing || refreshCooldown > 0}
            className={`flex items-center gap-2 px-3 py-1.5 rounded-lg text-sm font-medium transition ${
              isRefreshing || refreshCooldown > 0
                ? 'bg-gray-300 text-gray-500 cursor-not-allowed'
                : 'bg-blue-600 text-white hover:bg-blue-700 cursor-pointer'
            }`}
          >
            <RefreshCw className={`w-3.5 h-3.5 ${isRefreshing ? 'animate-spin' : ''}`} />
            {isRefreshing ? 'Polling...' : refreshCooldown > 0 ? `${refreshCooldown}s` : 'Refresh'}
          </button>
        </div>
      </div>

      <div className="overflow-x-auto border rounded-lg">
        <table className="w-full">
          <thead className="bg-gray-50 border-b">
            <tr>
              {['IPv6 Address', 'MAC Address', 'Interface', 'Source Router', 'State', 'Type', 'Policy Groups', 'Last Seen'].map(h => (
                <th key={h} className="px-4 py-2.5 text-left text-xs font-semibold text-gray-600 uppercase tracking-wide">
                  {h}
                </th>
              ))}
            </tr>
          </thead>
          <tbody className="divide-y divide-gray-100">
            {neighbors.length === 0 && (
              <tr>
                <td colSpan={8} className="px-4 py-12 text-center text-gray-400 text-sm">
                  No entries in ND cache. Click Refresh to reload.
                </td>
              </tr>
            )}
            {paginatedNeighbors.map(neighbor => {
              const neighborGroups = getNeighborGroups(neighbor.id);
              return (
                <tr key={neighbor.id} className="hover:bg-gray-50">
                  <td className="px-4 py-3 text-sm font-mono">{neighbor.id}</td>
                  <td className="px-4 py-3 text-sm font-mono text-gray-600">{neighbor.lladdr}</td>
                  <td className="px-4 py-3 text-sm text-gray-600">{neighbor.ifname}</td>
                  <td className="px-4 py-3 text-sm text-gray-600 font-mono">
                    {neighbor.source || <span className="text-gray-300">—</span>}
                  </td>
                  <td className="px-4 py-3 text-sm">
                    <StatusLed
                      status={neighborLedStatus(neighbor.state)}
                      label={neighbor.state}
                    />
                  </td>
                  <td className="px-4 py-3 text-sm">
                    {neighbor.is_router === 1 ? (
                      <span className="px-2 py-0.5 text-xs rounded font-medium bg-blue-100 text-blue-700">
                        Router
                      </span>
                    ) : (
                      <span className="text-xs text-gray-400">Host</span>
                    )}
                  </td>
                  <td className="px-4 py-3 text-sm">
                    {neighborGroups.length > 0 ? (
                      <div className="flex flex-wrap gap-1">
                        {neighborGroups.map(g => (
                          <span key={g.id} className="px-2 py-0.5 text-xs rounded font-medium bg-purple-100 text-purple-700">
                            {g.name}
                          </span>
                        ))}
                      </div>
                    ) : (
                      <span className="text-gray-300 text-xs">—</span>
                    )}
                  </td>
                  <td className="px-4 py-3 text-xs text-gray-500">{formatDateTime(neighbor.updated_at)}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>

      {/* Pagination */}
      <div className="mt-3 flex items-center justify-between text-sm text-gray-500">
        <span>
          {neighbors.length === 0
            ? '0 entries'
            : `${startIndex + 1}–${Math.min(startIndex + itemsPerPage, neighbors.length)} of ${neighbors.length} entries`}
        </span>
        {totalPages > 1 && (
          <div className="flex items-center gap-1">
            <button onClick={() => setCurrentPage(1)} disabled={currentPage === 1}
              className="px-2 py-1 border rounded hover:bg-gray-50 disabled:opacity-40 text-xs">First</button>
            <button onClick={() => setCurrentPage(p => Math.max(1, p - 1))} disabled={currentPage === 1}
              className="px-2 py-1 border rounded hover:bg-gray-50 disabled:opacity-40 text-xs">Prev</button>
            {Array.from({ length: Math.min(5, totalPages) }, (_, i) => {
              const start = Math.max(1, Math.min(currentPage - 2, totalPages - 4));
              const page = start + i;
              return (
                <button key={page} onClick={() => setCurrentPage(page)}
                  className={`px-2 py-1 border rounded text-xs ${page === currentPage ? 'bg-blue-600 text-white border-blue-600' : 'hover:bg-gray-50'}`}>
                  {page}
                </button>
              );
            })}
            <button onClick={() => setCurrentPage(p => Math.min(totalPages, p + 1))} disabled={currentPage === totalPages}
              className="px-2 py-1 border rounded hover:bg-gray-50 disabled:opacity-40 text-xs">Next</button>
            <button onClick={() => setCurrentPage(totalPages)} disabled={currentPage === totalPages}
              className="px-2 py-1 border rounded hover:bg-gray-50 disabled:opacity-40 text-xs">Last</button>
          </div>
        )}
      </div>
    </div>
  );
}
