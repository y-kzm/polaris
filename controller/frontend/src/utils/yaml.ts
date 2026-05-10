import type { Rule, Group, RouterConfig, DiffLine } from '../types';

export function generateYaml(rules: Rule[], groups: Group[]): string {
  let yaml = 'rules:\n';

  rules.forEach(rule => {
    yaml += `  - id: ${rule.id}\n`;
    yaml += `    prefixes:\n`;

    rule.entries.forEach(entry => {
      if (entry.type === 'fqdn') {
        yaml += `      - "${entry.value}" # ${entry.service}\n`;
      } else {
        yaml += `      - "${entry.value}"\n`;
      }
    });

    yaml += `    nexthop: "${rule.nexthop}"\n`;
  });

  yaml += '\ngroups:\n';
  groups.forEach(group => {
    yaml += `  - id: ${group.id}\n`;
    yaml += `    name: "${group.name}"\n`;
    yaml += `    rules: [${group.rules.join(', ')}]\n`;
    yaml += `    members:\n`;
    group.members.forEach(member => {
      yaml += `      - "${member}"\n`;
    });
  });

  return yaml;
}

export function generateRouterConfigs(
  rules: Rule[],
  routers: RouterConfig[],
  groups: Group[]
): Record<string, string> {
  const files: Record<string, string> = {};

  routers.forEach((router) => {
    const routerRules = rules.filter(
      (rule) => rule.nexthop === router.id || rule.nexthop === router.address
    );

    routerRules.forEach((rule) => {
      if (groups.filter((g) => g.rules.includes(rule.id)).length === 0) {
        return;
      }

      let config = '';
      config += `interface ${router.interface} {\n`;

      const isDefault = rule.entries.length === 1 && rule.entries[0].value === '::/0';
      const preference = isDefault ? 'high' : 'medium';
      config += `  AdvDefaultPreference ${preference};\n`;

      if (!isDefault) {
        rule.entries.forEach((entry) => {
          config += `\n  route ${entry.value} {\n`;
          config += `  };\n`;
        });
      }

      const groupsForThisRule = groups.filter((g) => g.rules.includes(rule.id));
      const memberSet = new Set<unknown>();
      groupsForThisRule.forEach((g) => g.members.forEach((m) => memberSet.add(m)));

      if (memberSet.size > 0) {
        config += `\n  clients {\n`;
        Array.from(memberSet).forEach((member) => {
          config += `    ${member};\n`;
        });
        config += `  };\n`;
      }

      config += `}\n`;

      const routerLabel = router.name || router.id || 'Router';
      const ruleLabel = rule.comment || `Rule${rule.id}`;
      const clean = (s: string) => s.replace(/\s+/g, '');
      const filename = `${clean(routerLabel)}-${clean(ruleLabel)}.conf`;

      files[filename] = config;
    });
  });

  return files;
}

// LCS-based diff: O(m*n) — fine for the typical <200 line policy YAML.
function lcsDiff(
  oldLines: string[],
  newLines: string[],
): Array<{ type: 'unchanged' | 'added' | 'removed'; content: string }> {
  const m = oldLines.length;
  const n = newLines.length;

  // Build LCS length table
  const dp: number[][] = Array.from({ length: m + 1 }, () => new Array(n + 1).fill(0));
  for (let i = 1; i <= m; i++) {
    for (let j = 1; j <= n; j++) {
      dp[i][j] = oldLines[i - 1] === newLines[j - 1]
        ? dp[i - 1][j - 1] + 1
        : Math.max(dp[i - 1][j], dp[i][j - 1]);
    }
  }

  // Backtrack to produce the edit script
  const result: Array<{ type: 'unchanged' | 'added' | 'removed'; content: string }> = [];
  let i = m, j = n;
  while (i > 0 || j > 0) {
    if (i > 0 && j > 0 && oldLines[i - 1] === newLines[j - 1]) {
      result.unshift({ type: 'unchanged', content: oldLines[i - 1] });
      i--; j--;
    } else if (j > 0 && (i === 0 || dp[i][j - 1] >= dp[i - 1][j])) {
      result.unshift({ type: 'added', content: newLines[j - 1] });
      j--;
    } else {
      result.unshift({ type: 'removed', content: oldLines[i - 1] });
      i--;
    }
  }
  return result;
}

export function generateYamlDiff(
  rules: Rule[],
  groups: Group[],
  previousYaml: string
): DiffLine[] {
  const currentYaml = generateYaml(rules, groups);
  const currentLines = currentYaml.split('\n').filter((_, i, a) => i < a.length - 1 || _ !== '');
  const previousLines = previousYaml.split('\n').filter((_, i, a) => i < a.length - 1 || _ !== '');

  const edits = lcsDiff(previousLines, currentLines);
  return edits.map((e, i) => ({ type: e.type, content: e.content, lineNum: i + 1 }));
}

export function hasChanges(
  rules: Rule[],
  groups: Group[],
  previousYaml: string
): boolean {
  if (!previousYaml) return false;
  const currentYaml = generateYaml(rules, groups);
  return currentYaml !== previousYaml;
}
