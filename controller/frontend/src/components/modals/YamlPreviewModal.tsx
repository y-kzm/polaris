import { useState } from 'react';
import type { Rule, Group, RouterConfig } from '../../types';
import { generateYaml, generateRouterConfigs } from '../../utils/yaml';

interface YamlPreviewModalProps {
  rules: Rule[];
  groups: Group[];
  routers: RouterConfig[];
  onClose: () => void;
}

export default function YamlPreviewModal({
  rules,
  groups,
  routers,
  onClose,
}: YamlPreviewModalProps) {
  // Active sub-tab: 'yaml' or 'router-configs'
  const [yamlPreviewTab, setYamlPreviewTab] = useState('yaml');

  const yaml = generateYaml(rules, groups);
  const routerConfigs = generateRouterConfigs(rules, routers, groups);

  return (
    <div className="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center p-4 z-50">
      <div className="bg-white rounded-lg shadow-xl max-w-6xl w-full max-h-[80vh] overflow-hidden">
        <div className="p-6 border-b">
          <h2 className="text-xl font-bold">Configuration Preview</h2>
          <p className="text-sm text-gray-600 mt-1">
            Policy YAML and per-router configurations
          </p>
        </div>

        {/* Sub-tab navigation */}
        <div className="border-b">
          <div className="flex px-6">
            <button
              onClick={() => setYamlPreviewTab('yaml')}
              className={`px-6 py-3 font-medium transition ${
                yamlPreviewTab === 'yaml'
                  ? 'text-blue-600 border-b-2 border-blue-600'
                  : 'text-gray-600 hover:text-gray-800'
              }`}
            >
              Policy YAML
            </button>
            <button
              onClick={() => setYamlPreviewTab('router-configs')}
              className={`px-6 py-3 font-medium transition ${
                yamlPreviewTab === 'router-configs'
                  ? 'text-blue-600 border-b-2 border-blue-600'
                  : 'text-gray-600 hover:text-gray-800'
              }`}
            >
              Router Configs ({Object.keys(routerConfigs).length})
            </button>
          </div>
        </div>

        <div className="overflow-y-auto max-h-[60vh] p-6">
          {/* Policy YAML tab */}
          {yamlPreviewTab === 'yaml' && (
            <>
              {/* YAML display area */}
              <div className="bg-gray-900 rounded-lg p-4 overflow-x-auto">
                <pre className="text-sm text-green-400 font-mono whitespace-pre">
                  {yaml}
                </pre>
              </div>

              {/* Configuration summary */}
              <div className="mt-4 p-4 bg-blue-50 rounded-lg">
                <h3 className="font-semibold text-sm text-blue-900 mb-2">Configuration Summary</h3>
                <div className="text-sm text-blue-800 space-y-1">
                  <div>• Total Rules: {rules.length}</div>
                  <div>• Total Groups: {groups.length}</div>
                  <div>• Total Members: {groups.reduce((sum, g) => sum + g.members.length, 0)}</div>
                  <div>• Active Routers: {routers.filter(r => r.status === 'active').length} / {routers.length}</div>
                </div>
              </div>
            </>
          )}

          {/* Per-router config tab */}
          {yamlPreviewTab === 'router-configs' && (
            <div className="space-y-4">
              {Object.entries(routerConfigs).map(([routerName, config]) => (
                <div key={routerName} className="border rounded-lg overflow-hidden">
                  <div className="bg-gray-100 px-4 py-2 border-b">
                    <h3 className="font-semibold text-gray-800">{routerName}</h3>
                  </div>
                  <div className="bg-gray-900 p-4 overflow-x-auto">
                    <pre className="text-sm text-green-400 font-mono whitespace-pre">
                      {config}
                    </pre>
                  </div>
                </div>
              ))}
            </div>
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
