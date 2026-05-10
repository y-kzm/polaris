import type { DiffLine } from '../../types';

interface YamlDiffModalProps {
  diffLines: DiffLine[];
  hasChanges: boolean;
  onClose: () => void;
}

export default function YamlDiffModal({ diffLines, hasChanges, onClose }: YamlDiffModalProps) {
  return (
    <div className="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center p-4 z-50">
      <div className="bg-white rounded-lg shadow-xl max-w-6xl w-full max-h-[80vh] overflow-hidden">
        <div className="p-6 border-b">
          <h2 className="text-xl font-bold">Configuration Changes</h2>
          <p className="text-sm text-gray-600 mt-1">
            {hasChanges
              ? 'Comparing current configuration with last applied policy'
              : 'No changes detected - current configuration matches last applied policy'}
          </p>
        </div>

        <div className="overflow-y-auto max-h-[60vh] p-6">
          {/* No-changes placeholder */}
          {!hasChanges ? (
            <div className="text-center py-12">
              <div className="text-6xl mb-4">✓</div>
              <div className="text-xl font-semibold text-gray-700 mb-2">No Changes Detected</div>
              <div className="text-gray-500">
                Your current configuration matches the last applied policy.
              </div>
            </div>
          ) : (
            <>
              {/* Diff header labels */}
              <div className="grid grid-cols-2 gap-4 mb-4">
                <div className="text-center p-2 bg-red-50 rounded">
                  <span className="text-sm font-semibold text-red-700">Previous (Applied)</span>
                </div>
                <div className="text-center p-2 bg-green-50 rounded">
                  <span className="text-sm font-semibold text-green-700">Current (Pending)</span>
                </div>
              </div>

              {/* Diff display area */}
              <div className="bg-gray-900 rounded-lg p-4 overflow-x-auto">
                <div className="font-mono text-sm">
                  {diffLines.map((line, idx) => (
                    <div
                      key={idx}
                      className={`leading-6 ${
                        line.type === 'added'
                          ? 'bg-green-900 bg-opacity-30 text-green-400'
                          : line.type === 'removed'
                          ? 'bg-red-900 bg-opacity-30 text-red-400'
                          : 'text-gray-400'
                      }`}
                    >
                      {/* Line number */}
                      <span className="inline-block w-12 text-right pr-4 text-gray-600 select-none">
                        {line.lineNum}
                      </span>
                      {/* Diff symbol (+/-/space) */}
                      <span className="inline-block w-8 text-center select-none">
                        {line.type === 'added' ? '+' : line.type === 'removed' ? '-' : ' '}
                      </span>
                      {/* Line content */}
                      <span className="whitespace-pre">{line.content || ' '}</span>
                    </div>
                  ))}
                </div>
              </div>

              {/* Diff statistics */}
              <div className="mt-4 grid grid-cols-3 gap-4">
                {/* Added lines count */}
                <div className="p-3 bg-green-50 rounded-lg">
                  <div className="text-xs font-semibold text-green-900 mb-1">Added Lines</div>
                  <div className="text-2xl font-bold text-green-700">
                    {diffLines.filter(l => l.type === 'added').length}
                  </div>
                </div>
                {/* Removed lines count */}
                <div className="p-3 bg-red-50 rounded-lg">
                  <div className="text-xs font-semibold text-red-900 mb-1">Removed Lines</div>
                  <div className="text-2xl font-bold text-red-700">
                    {diffLines.filter(l => l.type === 'removed').length}
                  </div>
                </div>
                {/* Unchanged lines count */}
                <div className="p-3 bg-blue-50 rounded-lg">
                  <div className="text-xs font-semibold text-blue-900 mb-1">Unchanged Lines</div>
                  <div className="text-2xl font-bold text-blue-700">
                    {diffLines.filter(l => l.type === 'unchanged').length}
                  </div>
                </div>
              </div>
            </>
          )}
        </div>

        {/* Modal footer */}
        <div className="p-6 border-t flex justify-end gap-3">
          <button
            onClick={onClose}
            className="px-4 py-2 bg-gray-600 text-white rounded-lg hover:bg-gray-700"
          >
            Close
          </button>
        </div>
      </div>
    </div>
  );
}
