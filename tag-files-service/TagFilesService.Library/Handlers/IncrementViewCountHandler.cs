using MediatR;
using TagFilesService.Infrastructure;
using TagFilesService.Library.Contracts;
using TagFilesService.Model;
using TagFilesService.Model.Exceptions;

namespace TagFilesService.Library.Handlers;

public class IncrementViewCountHandler(AppDbContext dbContext) : IRequestHandler<IncrementViewCountRequest>
{
    public async Task Handle(IncrementViewCountRequest request, CancellationToken cancellationToken)
    {
        LibraryItem? item = await dbContext.LibraryItems.FindAsync([request.LibraryItemId], cancellationToken);
        if (item is null)
        {
            throw new NotFoundException("LibraryItem", request.LibraryItemId.ToString());
        }

        item.IncrementViewCount();
        await dbContext.SaveChangesAsync(cancellationToken);
    }
}