using System.Collections.Concurrent;

namespace TagFilesService.FilesProcessing;

public class VideoConversionQueue
{
    public void Enqueue(string fileName)
    {
        _queue.Enqueue(fileName);
    }

    public bool TryDequeue(out string? fileName)
    {
        return _queue.TryDequeue(out fileName);
    }

    // TODO: Try Channel<T> instead of ConcurrentQueue<T>
    private readonly ConcurrentQueue<string> _queue = new();
}