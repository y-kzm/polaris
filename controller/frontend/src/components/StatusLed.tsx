type LedStatus = 'up' | 'down' | 'warning' | 'unknown';

interface StatusLedProps {
  status: LedStatus;
  label?: string;
}

const dotColors: Record<LedStatus, string> = {
  up:      'bg-green-500',
  down:    'bg-red-500',
  warning: 'bg-yellow-400',
  unknown: 'bg-gray-400',
};

const labelColors: Record<LedStatus, string> = {
  up:      'text-green-700',
  down:    'text-red-700',
  warning: 'text-yellow-700',
  unknown: 'text-gray-500',
};

export type { LedStatus };

export default function StatusLed({ status, label }: StatusLedProps) {
  return (
    <span className="inline-flex items-center gap-1.5">
      <span className={`w-2 h-2 rounded-full flex-shrink-0 ${dotColors[status]}`} />
      {label && <span className={`text-xs font-medium ${labelColors[status]}`}>{label}</span>}
    </span>
  );
}
