import { Toaster } from "@/components/ui/toaster";
import { Toaster as Sonner } from "@/components/ui/sonner";
import { TooltipProvider } from "@/components/ui/tooltip";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { BrowserRouter, Routes, Route, Navigate } from "react-router-dom";
import { AuthProvider, useAuth } from "./contexts/AuthContext";
import AppLayout from "./components/Layout/AppLayout";
import Dashboard from "./pages/Dashboard";
import DCPLibrary from "./pages/DCPLibrary";
import Servers from "./pages/Servers";
import Transfers from "./pages/Transfers";
import Torrents from "./pages/Torrents";
import TorrentStatus from "./pages/TorrentStatus";
import TrackerLive from "./pages/TrackerLive";
import Analytics from "./pages/Analytics";
import Settings from "./pages/Settings";
import Login from "./pages/Login";
import NotFound from "./pages/NotFound";
import { Cloud } from "lucide-react";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: 2,
      staleTime: 10000,
    },
  },
});

/** Shows a loading spinner while the auth state is being resolved */
function AuthLoading() {
  return (
    <div className="min-h-screen flex items-center justify-center bg-gradient-to-br from-slate-50 via-blue-50 to-slate-100">
      <div className="flex flex-col items-center gap-4">
        <div className="flex h-14 w-14 items-center justify-center rounded-2xl bg-gradient-to-br from-blue-500 to-blue-700 shadow-lg shadow-blue-500/30 animate-pulse">
          <Cloud className="h-8 w-8 text-white" />
        </div>
        <p className="text-sm text-gray-500">Loading...</p>
      </div>
    </div>
  );
}

/** Redirects to /login when not authenticated */
function RequireAuth({ children }: { children: React.ReactNode }) {
  const { isAuthenticated, isLoading } = useAuth();
  if (isLoading) return <AuthLoading />;
  if (!isAuthenticated) return <Navigate to="/login" replace />;
  return <>{children}</>;
}

/** Redirects to / when already authenticated */
function GuestOnly({ children }: { children: React.ReactNode }) {
  const { isAuthenticated, isLoading } = useAuth();
  if (isLoading) return <AuthLoading />;
  if (isAuthenticated) return <Navigate to="/" replace />;
  return <>{children}</>;
}

/** Redirects to / if the user lacks access to the given page */
function RequirePermission({ page, children }: { page: string; children: React.ReactNode }) {
  const { canAccess } = useAuth();
  if (!canAccess(page)) return <Navigate to="/" replace />;
  return <>{children}</>;
}

const App = () => (
  <QueryClientProvider client={queryClient}>
    <AuthProvider>
      <TooltipProvider>
        <Toaster />
        <Sonner />
        <BrowserRouter>
          <Routes>
            {/* Login page - accessible only when logged out */}
            <Route path="/login" element={<GuestOnly><Login /></GuestOnly>} />

            {/* Protected routes - require authentication */}
            <Route element={<RequireAuth><AppLayout /></RequireAuth>}>
              <Route path="/" element={<Dashboard />} />
              <Route path="/dcps" element={<RequirePermission page="dcps"><DCPLibrary /></RequirePermission>} />
              <Route path="/servers" element={<RequirePermission page="servers"><Servers /></RequirePermission>} />
              <Route path="/sites" element={<RequirePermission page="servers"><Servers /></RequirePermission>} />
              <Route path="/transfers" element={<RequirePermission page="transfers"><Transfers /></RequirePermission>} />
              <Route path="/torrents" element={<RequirePermission page="torrents"><Torrents /></RequirePermission>} />
              <Route path="/torrent-status" element={<RequirePermission page="torrent-status"><TorrentStatus /></RequirePermission>} />
              <Route path="/tracker" element={<RequirePermission page="tracker"><TrackerLive /></RequirePermission>} />
              <Route path="/analytics" element={<RequirePermission page="analytics"><Analytics /></RequirePermission>} />
              <Route path="/settings" element={<RequirePermission page="settings"><Settings /></RequirePermission>} />
            </Route>

            <Route path="*" element={<NotFound />} />
          </Routes>
        </BrowserRouter>
      </TooltipProvider>
    </AuthProvider>
  </QueryClientProvider>
);

export default App;
