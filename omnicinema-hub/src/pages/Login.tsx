import { useState, useEffect, useRef } from 'react';
import { useNavigate } from 'react-router-dom';
import { Cloud, Lock, User, Eye, EyeOff } from 'lucide-react';
import { useAuth } from '@/contexts/AuthContext';

export default function Login() {
  const { login } = useAuth();
  const navigate = useNavigate();
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [showPassword, setShowPassword] = useState(false);
  const [error, setError] = useState('');
  const [isLoading, setIsLoading] = useState(false);
  const [loginSuccess, setLoginSuccess] = useState(false);
  const canvasRef = useRef<HTMLCanvasElement>(null);

  // Cloud particle animation
  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const ctx = canvas.getContext('2d');
    if (!ctx) return;

    let animationId: number;
    let particles: CloudParticle[] = [];

    const resize = () => {
      canvas.width = window.innerWidth;
      canvas.height = window.innerHeight;
    };
    resize();
    window.addEventListener('resize', resize);

    class CloudParticle {
      x: number;
      y: number;
      size: number;
      speedX: number;
      speedY: number;
      opacity: number;
      targetOpacity: number;

      constructor(w: number, h: number) {
        this.x = Math.random() * w;
        this.y = Math.random() * h;
        this.size = Math.random() * 80 + 30;
        this.speedX = (Math.random() - 0.5) * 0.3;
        this.speedY = (Math.random() - 0.5) * 0.15;
        this.opacity = Math.random() * 0.08 + 0.02;
        this.targetOpacity = this.opacity;
      }

      update(w: number, h: number, boosted: boolean) {
        this.x += this.speedX;
        this.y += this.speedY;
        if (this.x < -this.size) this.x = w + this.size;
        if (this.x > w + this.size) this.x = -this.size;
        if (this.y < -this.size) this.y = h + this.size;
        if (this.y > h + this.size) this.y = -this.size;

        if (boosted) {
          this.targetOpacity = Math.min(this.opacity * 4, 0.35);
          this.speedX *= 1.01;
        }
        this.opacity += (this.targetOpacity - this.opacity) * 0.02;
      }

      draw(ctx: CanvasRenderingContext2D) {
        const gradient = ctx.createRadialGradient(this.x, this.y, 0, this.x, this.y, this.size);
        gradient.addColorStop(0, `rgba(59, 130, 246, ${this.opacity})`);
        gradient.addColorStop(0.5, `rgba(96, 165, 250, ${this.opacity * 0.5})`);
        gradient.addColorStop(1, `rgba(147, 197, 253, 0)`);
        ctx.beginPath();
        ctx.arc(this.x, this.y, this.size, 0, Math.PI * 2);
        ctx.fillStyle = gradient;
        ctx.fill();
      }
    }

    // Create particles
    const count = Math.floor((canvas.width * canvas.height) / 15000);
    for (let i = 0; i < count; i++) {
      particles.push(new CloudParticle(canvas.width, canvas.height));
    }

    const animate = () => {
      ctx.clearRect(0, 0, canvas.width, canvas.height);
      for (const p of particles) {
        p.update(canvas.width, canvas.height, loginSuccess);
        p.draw(ctx);
      }
      animationId = requestAnimationFrame(animate);
    };
    animate();

    return () => {
      cancelAnimationFrame(animationId);
      window.removeEventListener('resize', resize);
    };
  }, [loginSuccess]);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError('');
    setIsLoading(true);

    try {
      await login(username, password);
      setLoginSuccess(true);
      // Wait for the cloud animation to play before navigating
      setTimeout(() => navigate('/', { replace: true }), 1500);
    } catch (err: any) {
      const msg = err?.response?.data?.error || err?.response?.data?.message || 'Login failed';
      setError(msg);
      setIsLoading(false);
    }
  };

  return (
    <div className="relative min-h-screen flex items-center justify-center overflow-hidden bg-gradient-to-br from-slate-50 via-blue-50 to-slate-100">
      {/* Animated cloud background */}
      <canvas
        ref={canvasRef}
        className="absolute inset-0 pointer-events-none"
      />

      {/* Success flash overlay */}
      <div
        className={`absolute inset-0 bg-blue-500 transition-opacity duration-1000 pointer-events-none z-10 ${
          loginSuccess ? 'opacity-20' : 'opacity-0'
        }`}
      />

      {/* Login card */}
      <div
        className={`relative z-20 w-full max-w-md mx-4 transition-all duration-700 ${
          loginSuccess ? 'scale-95 opacity-0 translate-y-[-20px]' : 'scale-100 opacity-100'
        }`}
      >
        <div className="bg-white/80 backdrop-blur-xl rounded-2xl shadow-2xl shadow-blue-500/10 border border-white/50 p-8">
          {/* Logo */}
          <div className="flex flex-col items-center mb-8">
            <div className={`flex h-16 w-16 items-center justify-center rounded-2xl bg-gradient-to-br from-blue-500 to-blue-700 shadow-lg shadow-blue-500/30 mb-4 transition-transform duration-500 ${
              loginSuccess ? 'scale-150 rotate-12' : ''
            }`}>
              <Cloud className={`h-9 w-9 text-white transition-transform duration-500 ${loginSuccess ? 'scale-110' : ''}`} />
            </div>
            <h1 className="text-2xl font-bold text-gray-900">OmniCloud</h1>
            <p className="text-sm text-gray-500 mt-1">Cinema Distribution Platform</p>
          </div>

          {/* Form */}
          <form onSubmit={handleSubmit} className="space-y-5">
            {/* Username */}
            <div>
              <label htmlFor="username" className="block text-sm font-medium text-gray-700 mb-1.5">
                Username
              </label>
              <div className="relative">
                <User className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-gray-400" />
                <input
                  id="username"
                  type="text"
                  value={username}
                  onChange={e => setUsername(e.target.value)}
                  className="w-full pl-10 pr-4 py-2.5 rounded-xl border border-gray-200 bg-white/70 text-gray-900 placeholder-gray-400 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-400 transition-all"
                  placeholder="Enter your username"
                  autoComplete="username"
                  autoFocus
                  disabled={isLoading || loginSuccess}
                />
              </div>
            </div>

            {/* Password */}
            <div>
              <label htmlFor="password" className="block text-sm font-medium text-gray-700 mb-1.5">
                Password
              </label>
              <div className="relative">
                <Lock className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-gray-400" />
                <input
                  id="password"
                  type={showPassword ? 'text' : 'password'}
                  value={password}
                  onChange={e => setPassword(e.target.value)}
                  className="w-full pl-10 pr-11 py-2.5 rounded-xl border border-gray-200 bg-white/70 text-gray-900 placeholder-gray-400 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-400 transition-all"
                  placeholder="Enter your password"
                  autoComplete="current-password"
                  disabled={isLoading || loginSuccess}
                />
                <button
                  type="button"
                  onClick={() => setShowPassword(!showPassword)}
                  className="absolute right-3 top-1/2 -translate-y-1/2 text-gray-400 hover:text-gray-600 transition-colors"
                  tabIndex={-1}
                >
                  {showPassword ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                </button>
              </div>
            </div>

            {/* Error message */}
            {error && (
              <div className="flex items-center gap-2 px-4 py-2.5 rounded-xl bg-red-50 border border-red-200 text-red-700 text-sm animate-in fade-in slide-in-from-top-1 duration-200">
                <div className="h-1.5 w-1.5 rounded-full bg-red-500 flex-shrink-0" />
                {error}
              </div>
            )}

            {/* Submit button */}
            <button
              type="submit"
              disabled={isLoading || loginSuccess || !username || !password}
              className={`w-full py-2.5 px-4 rounded-xl font-medium text-white transition-all duration-300 ${
                isLoading || loginSuccess
                  ? 'bg-blue-400 cursor-not-allowed'
                  : 'bg-gradient-to-r from-blue-500 to-blue-600 hover:from-blue-600 hover:to-blue-700 active:scale-[0.98] shadow-lg shadow-blue-500/25 hover:shadow-blue-500/40'
              } disabled:opacity-60`}
            >
              {loginSuccess ? (
                <span className="flex items-center justify-center gap-2">
                  <svg className="h-5 w-5 text-white" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={3}>
                    <path strokeLinecap="round" strokeLinejoin="round" d="M5 13l4 4L19 7" />
                  </svg>
                  Welcome back!
                </span>
              ) : isLoading ? (
                <span className="flex items-center justify-center gap-2">
                  <svg className="animate-spin h-5 w-5 text-white" xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24">
                    <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                    <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z" />
                  </svg>
                  Signing in...
                </span>
              ) : (
                'Sign In'
              )}
            </button>
          </form>

          {/* Footer */}
          <div className="mt-6 text-center">
            <p className="text-xs text-gray-400">Secure access to your cinema network</p>
          </div>
        </div>
      </div>

      {/* Success cloud burst animation */}
      {loginSuccess && (
        <div className="absolute inset-0 z-30 pointer-events-none flex items-center justify-center">
          {[...Array(12)].map((_, i) => (
            <div
              key={i}
              className="absolute animate-ping"
              style={{
                animationDelay: `${i * 100}ms`,
                animationDuration: '1.5s',
              }}
            >
              <Cloud
                className="text-blue-400"
                style={{
                  width: `${20 + Math.random() * 30}px`,
                  height: `${20 + Math.random() * 30}px`,
                  opacity: 0.3 - i * 0.02,
                  transform: `translate(${Math.cos(i * 30 * Math.PI / 180) * (50 + i * 20)}px, ${Math.sin(i * 30 * Math.PI / 180) * (50 + i * 20)}px)`,
                }}
              />
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
