using System.Threading.Channels;

namespace TagFilesService.FilesProcessing;

public class FilesProcessingQueue
{
    public async Task Enqueue(FileProcessingInfo info, CancellationToken cancellationToken)
    {
        await _queue.Writer.WriteAsync(info, cancellationToken);
    }

    public async Task<FileProcessingInfo> Dequeue(CancellationToken cancellationToken)
    {
        return await _queue.Reader.ReadAsync(cancellationToken);
    }

    private readonly Channel<FileProcessingInfo> _queue = Channel.CreateUnbounded<FileProcessingInfo>();
}