import { createContext, useContext, useState, useEffect, useCallback, ReactNode } from 'react';
import { authApi, LoginResponse } from '@/api/auth';

interface AuthState {
  isAuthenticated: boolean;
  isLoading: boolean;
  username: string | null;
  role: string | null;
  allowedPages: string[];
}

interface AuthContextType extends AuthState {
  login: (username: string, password: string) => Promise<LoginResponse>;
  logout: () => Promise<void>;
  canAccess: (page: string) => boolean;
}

const AuthContext = createContext<AuthContextType | null>(null);

const TOKEN_KEY = 'omnicloud_session_token';

const EMPTY: AuthState = {
  isAuthenticated: false,
  isLoading: false,
  username: null,
  role: null,
  allowedPages: [],
};

export function getStoredToken(): string | null {
  return localStorage.getItem(TOKEN_KEY);
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<AuthState>({
    ...EMPTY,
    isLoading: true,
  });

  // Check session on mount
  useEffect(() => {
    const token = getStoredToken();
    if (!token) {
      setState(s => ({ ...s, isLoading: false }));
      return;
    }

    authApi.checkSession()
      .then(res => {
        if (res.authenticated) {
          setState({
            isAuthenticated: true,
            isLoading: false,
            username: res.username ?? null,
            role: res.role ?? null,
            allowedPages: res.allowed_pages ?? [],
          });
        } else {
          localStorage.removeItem(TOKEN_KEY);
          setState(EMPTY);
        }
      })
      .catch(() => {
        localStorage.removeItem(TOKEN_KEY);
        setState(EMPTY);
      });
  }, []);

  const login = useCallback(async (username: string, password: string) => {
    const res = await authApi.login(username, password);
    localStorage.setItem(TOKEN_KEY, res.token);
    setState({
      isAuthenticated: true,
      isLoading: false,
      username: res.username,
      role: res.role,
      allowedPages: res.allowed_pages ?? [],
    });
    return res;
  }, []);

  const logout = useCallback(async () => {
    try {
      await authApi.logout();
    } catch {
      // ignore network errors on logout
    }
    localStorage.removeItem(TOKEN_KEY);
    setState(EMPTY);
  }, []);

  const canAccess = useCallback(
    (page: string) => {
      if (state.role === 'admin') return true;
      return state.allowedPages.includes(page);
    },
    [state.role, state.allowedPages],
  );

  return (
    <AuthContext.Provider value={{ ...state, login, logout, canAccess }}>
      {children}
    </AuthContext.Provider>
  );
}

export function useAuth() {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error('useAuth must be used within AuthProvider');
  return ctx;
}
