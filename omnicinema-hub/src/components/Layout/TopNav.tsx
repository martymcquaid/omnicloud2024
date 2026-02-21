import { NavLink, useLocation, useNavigate } from 'react-router-dom';
import { Film, Server, ArrowLeftRight, Hash, Activity, BarChart3, Cloud, Settings, User, Radio, LogOut } from 'lucide-react';
import { cn } from '@/lib/utils';
import { useHealth } from '@/hooks/useHealth';
import { useAuth } from '@/contexts/AuthContext';
import { ROLE_LABELS } from '@/lib/permissions';
import { useState, useEffect, useRef } from 'react';

const navItems = [
  { to: '/dcps', icon: Film, label: 'Content', page: 'dcps' },
  { to: '/servers', icon: Server, label: 'Sites', page: 'servers' },
  { to: '/transfers', icon: ArrowLeftRight, label: 'Transfers', page: 'transfers' },
  { to: '/torrents', icon: Hash, label: 'Ingesting', page: 'torrents' },
  { to: '/torrent-status', icon: Activity, label: 'Transfer Status', page: 'torrent-status' },
  { to: '/tracker', icon: Radio, label: 'Tracker', page: 'tracker' },
  { to: '/analytics', icon: BarChart3, label: 'Analytics', page: 'analytics' },
];

export default function TopNav() {
  const location = useLocation();
  const navigate = useNavigate();
  const { data: health, isError } = useHealth();
  const { username, role, logout, canAccess } = useAuth();
  const isHealthy = health?.status === 'healthy' && !isError;
  const [currentTime, setCurrentTime] = useState(new Date());
  const [showUserMenu, setShowUserMenu] = useState(false);
  const menuRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const timer = setInterval(() => setCurrentTime(new Date()), 1000);
    return () => clearInterval(timer);
  }, []);

  // Close menu on outside click
  useEffect(() => {
    const handler = (e: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) {
        setShowUserMenu(false);
      }
    };
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, []);

  const formatTime = (date: Date) => {
    return date.toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit', second: '2-digit' });
  };

  const formatDate = (date: Date) => {
    const day = date.toLocaleDateString('en-GB', { weekday: 'short' });
    const dayNum = date.getDate();
    const month = date.toLocaleDateString('en-GB', { month: 'short' });
    return `${day} ${dayNum} ${month}`;
  };

  const handleLogout = async () => {
    setShowUserMenu(false);
    await logout();
    navigate('/login', { replace: true });
  };

  return (
    <header className="sticky top-0 z-50 bg-white border-b border-gray-200 shadow-sm">
      {/* Main header bar */}
      <div className="flex items-center h-16 px-6">
        {/* Logo and Branding */}
        <div className="flex items-center gap-3">
          <div className="flex h-10 w-10 items-center justify-center rounded bg-blue-600">
            <Cloud className="h-6 w-6 text-white" />
          </div>
          <div className="flex flex-col">
            <span className="text-lg font-bold text-gray-900">OmniCloud</span>
            <span className="text-[10px] text-gray-500 tracking-wide">Cinema Distribution</span>
          </div>
        </div>

        {/* Center - Time and Navigation */}
        <div className="flex-1 flex items-center gap-8 ml-8">
          {/* Time Display */}
          <div className="text-xs font-mono text-gray-600 whitespace-nowrap border-r border-gray-300 pr-8">
            <div className="font-semibold text-gray-900">{formatTime(currentTime)}</div>
            <div className="text-gray-500">{formatDate(currentTime)}</div>
          </div>

          {/* Navigation */}
          <nav className="flex items-center gap-0.5">
            {navItems.filter(item => canAccess(item.page)).map(item => {
              const isActive = item.to === '/' ? location.pathname === '/' : location.pathname.startsWith(item.to);
              return (
                <NavLink
                  key={item.to}
                  to={item.to}
                  className={cn(
                    "flex items-center gap-2 px-4 py-2 text-sm font-medium transition-all rounded-md",
                    isActive
                      ? "bg-blue-50 text-blue-600 border-b-2 border-blue-600"
                      : "text-gray-600 hover:text-gray-900 hover:bg-gray-50"
                  )}
                >
                  <item.icon className="h-4 w-4" />
                  <span>{item.label}</span>
                </NavLink>
              );
            })}
          </nav>
        </div>

        {/* Right section - Status and Actions */}
        <div className="flex items-center gap-4 ml-auto">
          {/* Health Status */}
          <div className={cn(
            "flex items-center gap-2 px-3 py-1.5 rounded-full text-xs font-medium",
            isHealthy
              ? "bg-green-50 text-green-700 border border-green-200"
              : "bg-red-50 text-red-700 border border-red-200"
          )}>
            <div className={cn(
              "h-2 w-2 rounded-full",
              isHealthy ? "bg-green-500" : "bg-red-500"
            )} />
            <span>{isHealthy ? 'Online' : 'Offline'}</span>
          </div>

          {/* Action buttons */}
          {canAccess('settings') && (
            <NavLink
              to="/settings"
              className={cn(
                "p-2 rounded-lg transition-colors",
                location.pathname === '/settings'
                  ? "text-blue-600 bg-blue-50"
                  : "text-gray-600 hover:text-gray-900 hover:bg-gray-100"
              )}
            >
              <Settings className="h-5 w-5" />
            </NavLink>
          )}

          {/* User menu */}
          <div className="relative" ref={menuRef}>
            <button
              onClick={() => setShowUserMenu(!showUserMenu)}
              className="flex items-center gap-2 px-2 py-1 rounded-full hover:ring-2 hover:ring-blue-200 transition-all"
            >
              <div className="w-8 h-8 rounded-full bg-gradient-to-br from-blue-600 to-blue-400 flex items-center justify-center">
                <User className="h-5 w-5 text-white" />
              </div>
              <span className="text-sm font-medium text-gray-700 hidden lg:inline">{username}</span>
            </button>

            {showUserMenu && (
              <div className="absolute right-0 top-full mt-2 w-48 bg-white rounded-xl shadow-xl border border-gray-200 py-1 z-50 animate-in fade-in slide-in-from-top-2 duration-150">
                <div className="px-4 py-2.5 border-b border-gray-100">
                  <p className="text-sm font-medium text-gray-900">{username}</p>
                  <p className="text-xs text-gray-500">{ROLE_LABELS[role ?? ''] ?? role}</p>
                </div>
                <button
                  onClick={handleLogout}
                  className="w-full flex items-center gap-2 px-4 py-2.5 text-sm text-red-600 hover:bg-red-50 transition-colors"
                >
                  <LogOut className="h-4 w-4" />
                  Sign Out
                </button>
              </div>
            )}
          </div>
        </div>
      </div>
    </header>
  );
}
