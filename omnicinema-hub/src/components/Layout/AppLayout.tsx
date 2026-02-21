import { Outlet, useLocation } from 'react-router-dom';
import TopNav from './TopNav';

// Pages that need full-width layout (no container)
const fullWidthPages = ['/dcps'];

export default function AppLayout() {
  const location = useLocation();
  const isFullWidth = fullWidthPages.some(path => location.pathname.startsWith(path));

  return (
    <div className="min-h-screen bg-white">
      <TopNav />
      {isFullWidth ? (
        <main>
          <Outlet />
        </main>
      ) : (
        <main className="mx-auto max-w-[1400px] px-4 py-6 sm:px-6 lg:px-8">
          <Outlet />
        </main>
      )}
    </div>
  );
}
