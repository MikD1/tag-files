using MediatR;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Logging;
using TagFilesService.Infrastructure;
using TagFilesService.Library.Contracts;
using TagFilesService.Model;

namespace TagFilesService.Library.Handlers;

public class GetSimilarLibraryItemsHandler(ILogger<GetSimilarLibraryItemsHandler> logger, AppDbContext dbContext)
    : IRequestHandler<GetSimilarLibraryItemsRequest, List<LibraryItemDto>>
{
    public async Task<List<LibraryItemDto>> Handle(GetSimilarLibraryItemsRequest request,
        CancellationToken cancellationToken)
    {
        LibraryItem? sourceItem = await dbContext.LibraryItems
            .Where(x => x.Id == request.Id)
            .Include(x => x.Tags)
            .SingleOrDefaultAsync(cancellationToken);
        if (sourceItem is null)
        {
            logger.LogWarning("Library item with ID {id} not found.", request.Id);
            return [];
        }

        if (sourceItem.Tags.Count == 0)
        {
            logger.LogInformation("Library item with ID {id} has no tags, returning empty list.", request.Id);
            return [];
        }

        List<uint> sourceTagIds = sourceItem.Tags.Select(t => t.Id).ToList();
        List<LibraryItem> similarItems = await dbContext.LibraryItems
            .Where(x => x.Id != request.Id)
            .Include(x => x.Tags)
            .Where(x => x.Tags.Count == sourceItem.Tags.Count)
            .Where(x => x.Tags.All(t => sourceTagIds.Contains(t.Id)))
            .OrderByDescending(x => x.UploadedOn)
            .ToListAsync(cancellationToken);

        return similarItems.Select(LibraryItemDto.FromModel).ToList();
    }
}