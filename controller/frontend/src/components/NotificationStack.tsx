import { X, CheckCircle, AlertCircle, Info } from 'lucide-react';

export interface NotificationItem {
  id: number;
  type: 'success' | 'error' | 'info';
  message: string;
}

interface NotificationStackProps {
  notifications: NotificationItem[];
  onDismiss: (id: number) => void;
}

const styles = {
  success: { bg: 'bg-green-50 border-green-200', Icon: CheckCircle, iconColor: 'text-green-500', text: 'text-green-800' },
  error:   { bg: 'bg-red-50 border-red-200',     Icon: AlertCircle,  iconColor: 'text-red-500',   text: 'text-red-800'   },
  info:    { bg: 'bg-blue-50 border-blue-200',   Icon: Info,         iconColor: 'text-blue-500',  text: 'text-blue-800'  },
};

export default function NotificationStack({ notifications, onDismiss }: NotificationStackProps) {
  if (notifications.length === 0) return null;
  return (
    <div className="fixed top-4 right-4 z-50 flex flex-col gap-2 w-96 max-w-[calc(100vw-2rem)]">
      {notifications.map(n => {
        const s = styles[n.type];
        return (
          <div key={n.id} className={`flex items-start gap-3 p-4 border rounded-lg shadow-lg ${s.bg}`}>
            <s.Icon className={`w-5 h-5 flex-shrink-0 mt-0.5 ${s.iconColor}`} />
            <span className={`flex-1 text-sm ${s.text}`}>{n.message}</span>
            <button onClick={() => onDismiss(n.id)} className="text-gray-400 hover:text-gray-600 flex-shrink-0">
              <X className="w-4 h-4" />
            </button>
          </div>
        );
      })}
    </div>
  );
}
