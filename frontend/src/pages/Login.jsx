import React from 'react';

export default function Login({ username, setUsername, password, setPassword, handleLogin, loginError }) {
  return (
    <div className="login-page">
      <form className="login-card" onSubmit={handleLogin}>
        <h1>Backup Dashboard</h1>
        <p className="login-sub">Đăng nhập để tiếp tục</p>
        <input
          type="text"
          placeholder="Tên đăng nhập"
          value={username}
          onChange={(e) => setUsername(e.target.value)}
          autoFocus
        />
        <input
          type="password"
          placeholder="Mật khẩu"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
        {loginError && <div className="login-error">{loginError}</div>}
        <button type="submit" className="btn btn-primary login-btn">Đăng nhập</button>
      </form>
    </div>
  );
}
