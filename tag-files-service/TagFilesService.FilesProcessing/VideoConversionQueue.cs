using System.Threading.Channels;

namespace TagFilesService.FilesProcessing;

public class VideoConversionQueue
{
    public async Task Enqueue(string fileName, CancellationToken cancellationToken)
    {
        await _queue.Writer.WriteAsync(fileName, cancellationToken);
    }

    public async Task<string> Dequeue(CancellationToken cancellationToken)
    {
        return await _queue.Reader.ReadAsync(cancellationToken);
    }

    private readonly Channel<string> _queue = Channel.CreateUnbounded<string>();
}