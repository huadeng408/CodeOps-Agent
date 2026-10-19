import { useState } from 'react';
import { api } from './api';
import './LoginPage.css';

interface LoginPageProps {
  onLoginSuccess: () => void;
  staticPreview?: boolean;
}

export function LoginPage({ onLoginSuccess, staticPreview = false }: LoginPageProps) {
  const [isLogin, setIsLogin] = useState(true);
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [name, setName] = useState('');
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (staticPreview) return;
    setError('');
    setLoading(true);

    try {
      if (isLogin) {
        await api.login(email, password);
        onLoginSuccess();
      } else {
        await api.register(email, password, name);
        // Auto-login after registration
        await api.login(email, password);
        onLoginSuccess();
      }
    } catch (err: any) {
      setError(err.message || 'Authentication failed');
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="login-container">
      <div className="login-card">
        <h1>CodeOps-Agent</h1>
        {staticPreview && <p className="preview-notice" role="status">
          静态界面预览。登录、会话与代码任务请运行 Windows 本机产品。
          {' '}<a href="https://github.com/huadeng408/CodeOps-Agent#readme">项目与使用说明</a>
        </p>}
        <div className="login-tabs">
          <button
            className={isLogin ? 'active' : ''}
            onClick={() => setIsLogin(true)}
          >
            Login
          </button>
          <button
            className={!isLogin ? 'active' : ''}
            onClick={() => setIsLogin(false)}
          >
            Register
          </button>
        </div>

        <form onSubmit={handleSubmit}>
          {!isLogin && (
            <div className="form-group">
              <label>Name</label>
              <input
                type="text"
                value={name}
                onChange={(e) => setName(e.target.value)}
                required={!isLogin}
                placeholder="Your name"
                disabled={staticPreview}
              />
            </div>
          )}

          <div className="form-group">
            <label>Email</label>
            <input
              type="email"
                autoComplete="username"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              required
              placeholder="you@example.com"
              disabled={staticPreview}
            />
          </div>

          <div className="form-group">
            <label>Password</label>
            <input
              type="password"
              autoComplete={isLogin ? 'current-password' : 'new-password'}
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              required
              placeholder="••••••••"
              disabled={staticPreview}
            />
          </div>

          {error && <div className="error-message">{error}</div>}

          <button type="submit" className="submit-btn" disabled={loading || staticPreview}>
            {loading ? 'Please wait...' : isLogin ? 'Login' : 'Register'}
          </button>
        </form>
      </div>
    </div>
  );
}
