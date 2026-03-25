<?php
// Vxero Neo Download Server
// Usage:
//   curl -fsSL http://your-nas/neo/download.php?type=install | sh
//   curl -fsSL http://your-nas/neo/download.php?os=darwin&arch=arm64 -o neo

$type = $_GET['type'] ?? '';
$os   = $_GET['os'] ?? '';
$arch = $_GET['arch'] ?? '';

$dir = __DIR__;

// Serve the install script
if ($type === 'install') {
    $script = file_get_contents($dir . '/install.sh');
    header('Content-Type: text/plain');
    echo $script;
    exit;
}

// Serve a binary
if (!$os || !$arch) {
    http_response_code(400);
    echo "Missing os and arch params. Example: ?os=darwin&arch=arm64\n";
    exit;
}

// Whitelist valid combos
$valid = [
    'darwin-amd64'  => 'neo-darwin-amd64',
    'darwin-arm64'  => 'neo-darwin-arm64',
    'linux-amd64'   => 'neo-linux-amd64',
    'linux-arm64'   => 'neo-linux-arm64',
    'windows-amd64' => 'neo-windows-amd64.exe',
];

$key = "$os-$arch";
if (!isset($valid[$key])) {
    http_response_code(404);
    echo "Unsupported platform: $key\n";
    echo "Available: " . implode(', ', array_keys($valid)) . "\n";
    exit;
}

$file = $dir . '/' . $valid[$key];
if (!file_exists($file)) {
    http_response_code(404);
    echo "Binary not found: {$valid[$key]}\n";
    exit;
}

header('Content-Type: application/octet-stream');
header('Content-Disposition: attachment; filename="neo"');
header('Content-Length: ' . filesize($file));
readfile($file);
