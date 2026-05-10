import type { Neighbor } from '../../types';
import StatusLed from '../StatusLed';
import { neighborLedStatus } from '../../utils/format';

interface NeighborModalProps {
  neighbors: Neighbor[];
  selectedNeighbors: string[];
  lastCheckedIndex: number | null;
  sourceDescription: string;
  onSelectionChange: (neighbors: string[]) => void;
  onLastCheckedIndexChange: (index: number) => void;
  onClose: () => void;
}

export default function NeighborModal({
  neighbors,
  selectedNeighbors,
  lastCheckedIndex,
  sourceDescription,
  onSelectionChange,
  onLastCheckedIndexChange,
  onClose,
}: NeighborModalProps) {
  return (
    <div className="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center p-4 z-50">
      <div className="bg-white rounded-lg shadow-xl max-w-4xl w-full max-h-[80vh] overflow-hidden">
        <div className="p-6 border-b">
          <h2 className="text-lg font-bold text-gray-800">Select Clients from ND Cache</h2>
          <p className="text-xs text-gray-500 mt-1">Source: {sourceDescription}</p>
        </div>

        <div className="overflow-y-auto max-h-[60vh] p-6">
          {neighbors.length === 0 ? (
            <p className="text-sm text-gray-400 text-center py-8">No entries in ND cache.</p>
          ) : (
            <div className="space-y-1.5">
              {neighbors.map((neighbor, index) => (
                <label
                  key={neighbor.id}
                  className="flex items-start gap-3 p-3 border rounded-lg hover:bg-gray-50 cursor-pointer"
                >
                  <input
                    type="checkbox"
                    checked={selectedNeighbors.includes(neighbor.id)}
                    onChange={e => {
                      const checked = e.target.checked;
                      if ((e.nativeEvent as MouseEvent).shiftKey && lastCheckedIndex !== null) {
                        const start = Math.min(lastCheckedIndex, index);
                        const end = Math.max(lastCheckedIndex, index);
                        const rangeIds = neighbors.slice(start, end + 1).map(n => n.id);
                        onSelectionChange(
                          checked
                            ? Array.from(new Set([...selectedNeighbors, ...rangeIds]))
                            : selectedNeighbors.filter(id => !rangeIds.includes(id))
                        );
                      } else {
                        onSelectionChange(
                          checked
                            ? [...selectedNeighbors, neighbor.id]
                            : selectedNeighbors.filter(id => id !== neighbor.id)
                        );
                      }
                      onLastCheckedIndexChange(index);
                    }}
                    className="mt-1 rounded"
                  />
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2 mb-0.5 flex-wrap">
                      <span className="font-mono text-sm font-semibold text-gray-800">{neighbor.id}</span>
                      <StatusLed status={neighborLedStatus(neighbor.state)} label={neighbor.state} />
                      {neighbor.is_router === 1 && (
                        <span className="px-1.5 py-0.5 text-xs rounded bg-blue-100 text-blue-700">Router</span>
                      )}
                    </div>
                    <div className="text-xs text-gray-400 font-mono">{neighbor.lladdr}</div>
                  </div>
                </label>
              ))}
            </div>
          )}
        </div>

        <div className="p-6 border-t flex justify-between items-center">
          <span className="text-sm text-gray-500">
            {selectedNeighbors.length} selected
          </span>
          <div className="flex gap-3">
            <button
              onClick={() => { onClose(); onSelectionChange([]); }}
              className="px-4 py-2 border rounded-lg hover:bg-gray-50 text-sm"
            >
              Cancel
            </button>
            <button
              onClick={onClose}
              className="px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 text-sm font-medium"
            >
              Confirm ({selectedNeighbors.length})
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
