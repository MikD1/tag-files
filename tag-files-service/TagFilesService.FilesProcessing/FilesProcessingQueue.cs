using System.Threading.Channels;
using TagFilesService.Model.Processing;

namespace TagFilesService.FilesProcessing;

public class FilesProcessingQueue
{
    public async Task Enqueue(ProcessingFile processingFile, CancellationToken cancellationToken)
    {
        await _queue.Writer.WriteAsync(processingFile, cancellationToken);
    }

    public async Task<ProcessingFile> Dequeue(CancellationToken cancellationToken)
    {
        return await _queue.Reader.ReadAsync(cancellationToken);
    }

    private readonly Channel<ProcessingFile> _queue = Channel.CreateUnbounded<ProcessingFile>();
}