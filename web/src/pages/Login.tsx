// web/src/pages/Login.tsx
export default function Login() {
  return (
    <div className="flex h-screen items-center justify-center bg-gray-950">
      <div className="text-center">
        <h1 className="text-2xl font-bold text-white mb-2">CronFoundry</h1>
        <p className="text-gray-400 mb-6">Sign in to continue</p>
        <a
          href="/oauth/login"
          className="inline-flex items-center gap-2 rounded-lg bg-gray-800 px-4 py-2 text-white hover:bg-gray-700"
        >
          Sign in with GitHub
        </a>
      </div>
    </div>
  )
}
